"use client";

import Link from "next/link";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type Device, type DeviceStorage, hideDevice, unhideAll } from "@/lib/use-devices";
import { NetCaptureModal } from "./NetCaptureModal";
import { DeviceStorageFold } from "./DeviceStorageFold";
import WebShellModal from "@/components/dashboard/WebShellModal";
import { RecycleBoxDialog } from "@/components/dashboard/RecycleBoxDialog";
import { ManagedCloudSummary } from "@/components/dashboard/ManagedCloudPanel";
import WakeProgress, { ParkedSummary } from "@/components/dashboard/WakeProgress";
import { HIDE_PAID_UI } from "@/lib/launchFlags";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient, AgentClient, requestAgentUpdateViaConvex, type AgentUpdateStatus, type RunnerBrowserAuthSession, type RunnerTestResult } from "@/lib/agent-client";
import {
  lastSeenAgeMs,
  formatAgeShort,
  hasRecentLiveSignal,
  deriveDeviceLifecycleState,
  deriveBrowserReach,
  deviceStatusLabel,
  canBrowserActOnDevice,
  type BrowserReach,
  type DeviceLifecycleState,
} from "@/lib/device-lifecycle";
import { classifyTransport, fetchRelayHealth, type TransportInfo } from "@/lib/transport";
import {
  describeMachineState,
  isMachineRunning,
  isManagedCloudDevice,
  startManagedCloudMachine,
  stopManagedCloudMachine,
} from "@/lib/managed-cloud";
import { leaveSharedAccess } from "@/lib/guests";
import { classifyFetchError, type ClassifiedFailure } from "@/lib/connection-error";
import {
  probeAllowed,
  probeFailed,
  probeSucceeded,
  probeBackoffSecondsRemaining,
  probeReset,
  recordLastFailure,
  clearLastFailure,
  getLastFailure,
  subscribeLastFailure,
} from "@/lib/probe-backoff";

function transportToneClasses(tone: TransportInfo["tone"]): string {
  switch (tone) {
    case "emerald": return "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-200";
    case "blue":    return "border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-200";
    case "violet":  return "border-violet-300 bg-violet-50 text-violet-700 dark:border-violet-500/40 dark:bg-violet-500/10 dark:text-violet-200";
    case "amber":   return "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200";
    case "rose":    return "border-rose-300 bg-rose-50 text-rose-700 dark:border-rose-500/40 dark:bg-rose-500/10 dark:text-rose-200";
    default:        return "border-slate-300 bg-white text-slate-600 dark:border-surface-700 dark:bg-surface-800/40 dark:text-surface-300";
  }
}

function transportFor(device: Device): TransportInfo {
  // The dashboard only "owns" the relay/tunnel connection for the
  // device it's currently active against (deviceId in the relay
  // URL path matches). For every other device card we shouldn't
  // claim Yaver-public-relay just because the dashboard happens
  // to use that to reach a different device.
  const activeRelayUrl = agentClient.activeRelayUrl ?? null;
  const isActive = Boolean(
    activeRelayUrl &&
      activeRelayUrl.includes(`/d/${device.id}`),
  ) || Boolean(
    !activeRelayUrl && agentClient.connectionState === "connected",
  );
  return classifyTransport({
    host: device.host,
    port: device.port,
    localIps: device.localIps,
    publicEndpoints: device.publicEndpoints,
    tunnelUrl: device.tunnelUrl,
    activeRelayUrl: isActive ? activeRelayUrl : null,
    activeTunnelUrl: isActive ? agentClient.activeTunnelUrl ?? null : null,
    isActiveDevice: isActive,
    platform: device.platform,
    name: device.name,
  });
}

function sshSelectorForDevice(device: Pick<Device, "alias" | "id">): string {
  const alias = String(device.alias || "").trim();
  if (alias) return `@${alias}`;
  return device.id.slice(0, 8);
}

function stripSSHHost(raw: string | undefined): string {
  const text = String(raw || "").trim();
  if (!text) return "";
  try {
    if (text.startsWith("http://") || text.startsWith("https://")) {
      return new URL(text).host;
    }
  } catch {}
  return text.replace(/^https?:\/\//, "").replace(/\/+$/, "");
}

function isUsefulDirectSSHHost(host: string): boolean {
  return Boolean(
    host &&
      host !== "0.0.0.0" &&
      host !== "::" &&
      host !== "::1" &&
      !host.startsWith("127.") &&
      !/^172\.(1[6-9]|2\d|3[0-1])\.0\.1$/.test(host),
  );
}

function directSSHHostForDevice(device: Pick<Device, "publicEndpoints" | "localIps" | "host">): string {
  for (const endpoint of device.publicEndpoints || []) {
    const host = stripSSHHost(endpoint);
    if (isUsefulDirectSSHHost(host)) return host;
  }
  for (const ip of device.localIps || []) {
    if (/^100\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(ip)) return ip;
  }
  for (const ip of device.localIps || []) {
    if (isUsefulDirectSSHHost(ip)) return ip;
  }
  const host = stripSSHHost(device.host);
  if (isUsefulDirectSSHHost(host)) return host;
  return "";
}

function sshCommandForDevice(device: Pick<Device, "alias" | "id">): string {
  return `yaver ssh ${sshSelectorForDevice(device)}`;
}

function TransportBadge({ device }: { device: Device }) {
  const t = transportFor(device);
  return (
    <span
      className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${transportToneClasses(t.tone)}`}
      title={t.detail}
    >
      {t.label}
    </span>
  );
}

// The card shows WHICH OS as a glyph rather than a word: the platform is the
// one fact every card carries, and a row of identical monitors spends a whole
// icon slot saying nothing. Brand marks are recognised pre-attentively, which
// is the same reason the status dots aren't labelled either.
//
// This used to compare `platform === "iOS"` against device.platform, which the
// agent reports lowercase ("ios" / "darwin" / "linux"), so the branch never
// matched and macOS, Linux and Windows all fell through to one generic monitor.
// Normalise first, and give every platform its own mark.
function DeviceIcon({ platform, managed, label }: { platform: string; managed?: boolean; label?: string }) {
  const title = label ?? platformLabel(platform);
  const os = String(platform || "").trim().toLowerCase();

  // Yaver managed-cloud boxes get a cloud glyph regardless of the
  // underlying OS — they're "your cloud", not hardware you rack
  // yourself. Pairs with the "Yaver Managed Cloud" card badge.
  if (managed) {
    return (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" role="img" aria-label="Yaver managed cloud">
        <title>Yaver managed cloud</title>
        <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z" />
      </svg>
    );
  }

  // Brand marks are filled, not stroked — an Apple logo in outline reads as a
  // generic fruit. viewBox 24 matches the stroked glyphs around it.
  const brand = (d: string) => (
    <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor" role="img" aria-label={title}>
      <title>{title}</title>
      <path d={d} />
    </svg>
  );

  switch (os) {
    case "darwin":
    case "macos":
    case "ios":
      return brand(
        "M12.152 6.896c-.948 0-2.415-1.078-3.96-1.04-2.04.027-3.91 1.183-4.961 3.014-2.117 3.675-.546 9.103 1.519 12.09 1.013 1.454 2.208 3.09 3.792 3.039 1.52-.065 2.09-.987 3.935-.987 1.831 0 2.35.987 3.96.948 1.637-.026 2.676-1.48 3.676-2.948 1.156-1.688 1.636-3.325 1.662-3.415-.039-.013-3.182-1.221-3.22-4.857-.026-3.04 2.48-4.494 2.597-4.559-1.429-2.09-3.623-2.324-4.39-2.376-2-.156-3.675 1.09-4.61 1.09zM15.53 3.83c.843-1.012 1.4-2.427 1.245-3.83-1.207.052-2.662.805-3.532 1.818-.78.896-1.454 2.338-1.273 3.714 1.338.104 2.715-.688 3.559-1.701",
      );
    case "windows":
      return brand(
        "M0 3.449L9.75 2.1v9.451H0m10.949-9.602L24 0v11.4H10.949M0 12.6h9.75v9.451L0 20.699M10.949 12.6H24V24l-12.9-1.801",
      );
    case "android":
      return brand(
        "M17.523 15.3414c-.5511 0-.9993-.4486-.9993-.9997s.4482-.9993.9993-.9993c.5511 0 .9993.4482.9993.9993.0001.5511-.4482.9997-.9993.9997m-11.046 0c-.5511 0-.9993-.4486-.9993-.9997s.4482-.9993.9993-.9993c.5511 0 .9993.4482.9993.9993 0 .5511-.4482.9997-.9993.9997m11.4045-6.02l1.9973-3.4592a.416.416 0 00-.1521-.5676.416.416 0 00-.5676.1521l-2.0223 3.503C15.5902 8.2439 13.8533 7.8508 12 7.8508s-3.5902.3931-5.1367 1.0989L4.841 5.4467a.4161.4161 0 00-.5677-.1521.4157.4157 0 00-.1521.5676l1.9973 3.4592C2.6889 11.1867.3432 14.6589 0 18.761h24c-.3435-4.1021-2.6892-7.5743-6.1185-9.4396",
      );
    case "linux":
      return brand(
        "M12.504 0c-.155 0-.315.008-.48.021-4.226.333-3.105 4.807-3.17 6.298-.076 1.092-.3 1.953-1.05 3.02-.885 1.051-2.127 2.75-2.716 4.521-.278.832-.41 1.684-.287 2.489a.424.424 0 00-.11.135c-.26.268-.45.6-.663.839-.199.199-.485.267-.797.4-.313.136-.658.269-.864.68-.09.189-.136.394-.132.602 0 .199.027.4.055.6.058.399.116.728.04.978-.249.68-.28 1.145-.106 1.484.174.334.535.472.94.6.81.2 1.91.135 2.774.6.926.466 1.866.67 2.616.47.526-.116.97-.464 1.208-.946.587-.003 1.23-.269 2.26-.334.699-.058 1.574.267 2.577.2.025.134.063.198.114.333l.003.003c.391.778 1.113 1.132 1.884 1.071.771-.06 1.592-.536 2.257-1.306.631-.765 1.683-1.084 2.378-1.503.348-.199.629-.469.649-.853.023-.4-.2-.811-.714-1.376v-.097l-.003-.003c-.17-.2-.25-.535-.338-.926-.085-.401-.182-.786-.492-1.046h-.003c-.059-.054-.123-.067-.188-.135a.357.357 0 00-.19-.064c.431-1.278.264-2.55-.173-3.694-.533-1.41-1.465-2.638-2.175-3.483-.796-1.005-1.576-1.957-1.56-3.368.026-2.152.236-6.133-3.544-6.139z",
      );
  }

  // Unknown platform — the generic monitor is honest here, not a fallback we
  // forgot to fill in.
  return (
    <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" role="img" aria-label={title}>
      <title>{title}</title>
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

// isLikelyWSLDevice trusts the agent's authoritative WSL signal
// (set from /proc/version + WSL_DISTRO_NAME on the host itself, see
// agent's hardware_profile.go) when present. The earlier IP-based
// heuristic (172.16-31.x.y → "WSL NAT") false-positived on every
// real Linux box that picks a Docker bridge as its LAN IP — common on
// remote VPSes
// VMs, Pi devices with docker0, plain VPS — labelling them all as
// "Linux (likely WSL)". Hostname suffixes like "DESKTOP-" remain a
// soft fallback for older agents that haven't yet shipped isWsl.
function isLikelyWSLDevice(device: Pick<Device, "name" | "platform" | "hardwareProfile">): boolean {
  const platform = String(device.platform || "").trim().toLowerCase();
  if (platform !== "linux") return false;
  // Authoritative bit from the agent — trust it when present.
  if (device.hardwareProfile?.isWsl === true) return true;
  if (device.hardwareProfile?.isWsl === false) return false;
  // No isWsl reported (agent < 1.99.159 or hardware profile not yet
  // synced) → soft hostname-shape fallback. We deliberately stop at
  // hostname patterns; the IP-shape heuristic that this used to also
  // run is gone because Docker bridges trip it on every real Linux
  // box with containerd/docker installed.
  const name = String(device.name || "").trim().toUpperCase();
  return name.startsWith("DESKTOP-") || name.startsWith("LAPTOP-") || name.startsWith("WIN-");
}

function devicePlatformLabel(device: Pick<Device, "name" | "platform" | "hardwareProfile">): string {
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

function normalizeSemver(value: string | undefined | null): [number, number, number] | null {
  const raw = String(value || "").trim().replace(/^v/i, "");
  if (!raw) return null;
  const [major, minor, patch] = raw.split(".");
  const a = Number.parseInt(major || "0", 10);
  const b = Number.parseInt(minor || "0", 10);
  const c = Number.parseInt((patch || "0").replace(/[^0-9].*$/, ""), 10);
  if ([a, b, c].some((n) => Number.isNaN(n))) return null;
  return [a, b, c];
}

function isVersionOutdated(current: string | undefined | null, latest: string | undefined | null): boolean {
  const c = normalizeSemver(current);
  const l = normalizeSemver(latest);
  if (!c || !l) return false;
  if (l[0] !== c[0]) return l[0] > c[0];
  if (l[1] !== c[1]) return l[1] > c[1];
  return l[2] > c[2];
}

// isUsablePublicEndpoint — gate that filters out endpoints we
// know will fail before we even try them. Right now this is just
// the multi-level subdomain pattern <id>.dev.yaver.io: Cloudflare
// universal SSL only covers *.yaver.io (one level), so the
// wildcard cert for *.dev.yaver.io is missing. Probing those URLs
// from the dashboard fails at TLS handshake → "Could not connect
// to the server" / "access control checks" in console. The seed
// mutation populated 839 devices with these URLs ahead of cert
// provisioning; until the cert is actually wired (Cloudflare ACM
// or upload), keep the dashboard quiet by skipping them.
function isUsablePublicEndpoint(ep: string): boolean {
  // The two-label-deep wildcard cert blocker. <id>.dev.yaver.io
  // is the format the relay auto-mints. Anything not matching
  // that pattern (real Cloudflare tunnel, Tailscale serve URL,
  // user-configured custom domain) is fine and stays.
  if (/^https?:\/\/[^/]+\.dev\.yaver\.io(\/|$)/i.test(ep)) {
    return false;
  }
  return true;
}

// formatBytes — module-level helper for the AgentUpdateModal
// progress UI. Distinct from the local helper later in the file
// (`formatBytes` inside DeviceDetailsRow returns null for 0/-1).
// This one always returns a string.
function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "?";
  const k = 1024;
  if (n < k) return `${n} B`;
  if (n < k * k) return `${(n / k).toFixed(1)} KB`;
  if (n < k * k * k) return `${(n / (k * k)).toFixed(1)} MB`;
  return `${(n / (k * k * k)).toFixed(2)} GB`;
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
  "opencode",
] as const;

type RunnerHealth = "ready" | "needs-auth" | "down" | "not-installed" | "unknown";
type RunnerReportedStatus = "" | "running" | "idle" | "ready" | "needs-auth" | "needs_auth" | "down";

interface RunnerChipState {
  id: string;
  label: string;
  health: RunnerHealth;
  hint?: string;
}

function normalizeRunnerReportedStatus(status?: string): RunnerReportedStatus | "unknown" {
  switch (String(status || "").trim().toLowerCase()) {
    case "":
      return "";
    case "running":
    case "idle":
    case "ready":
    case "needs-auth":
    case "needs_auth":
    case "down":
      return String(status || "").trim().toLowerCase() as RunnerReportedStatus;
    default:
      return "unknown";
  }
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
    const reported = normalizeRunnerReportedStatus(status);
    switch (reported) {
      case "":
      case "idle":
      case "ready":
      case "running":
        return { id, label: id, health: "ready", hint: status };
      case "needs-auth":
      case "needs_auth":
        return { id, label: id, health: "needs-auth", hint: status };
      case "down":
        return { id, label: id, health: "down", hint: status };
      default:
        return { id, label: id, health: "unknown", hint: status };
    }
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
      return "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-200";
    case "needs-auth":
      return "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200";
    case "down":
      return "border-red-300 bg-red-50 text-red-700 dark:border-red-500/40 dark:bg-red-500/10 dark:text-red-200";
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
    case "needs-auth": return `${state.label}: installed but not signed in — click "Sign in" on this runner to authorize it with your Claude Max / ChatGPT Plus subscription`;
    case "down": return `${state.label}: detected but reporting an error: ${state.hint ?? "unknown"}`;
    case "not-installed": return `${state.label}: not installed on this machine`;
    default: return state.label;
  }
}

/**
 * RunnerChipWithTest renders one runner pill plus a "Test" CTA. The
 * Test button calls the Go agent's /agent/runners/test endpoint via a
 * per-card transient AgentClient (same pattern as RunnerAuthModal — we
 * don't want to clobber the workspace singleton, and we need to reach
 * the device whether the dashboard is currently connected to it or
 * not). On a `needsAuth + supportsBrowserAuth` result we automatically
 * trigger the existing headless sign-in modal so the user only ever
 * clicks once. Local LLMs (ollama / aider-ollama) skip that branch and
 * just render pass/fail — they have no browser-auth flow.
 */
function RunnerChipWithTest({
  device,
  state,
  token,
  onSignIn,
}: {
  device: Device;
  state: RunnerChipState;
  token: string | null;
  onSignIn: (runnerId: string) => void;
}) {
  type LocalState =
    | { kind: "idle" }
    | { kind: "running" }
    | { kind: "installing"; lastLine: string }
    | { kind: "install-ok" }
    | { kind: "install-fail"; message: string }
    | { kind: "ok"; result: RunnerTestResult }
    | { kind: "fail"; result: RunnerTestResult }
    | { kind: "error"; message: string };

  const [local, setLocal] = useState<LocalState>({ kind: "idle" });
  const inFlight = useRef(false);

  const supportsBrowserAuth = state.id === "claude" || state.id === "codex";
  const isLocalLLM = state.id === "ollama" || state.id === "aider-ollama";
  // Only owners can run a real generation against this machine — guests
  // would otherwise spend the host's API credit. Cloud LLMs need an
  // online device; local LLMs need the agent reachable too.
  const canTest =
    !!token &&
    !device.isGuest &&
    (device.online || device.workspaceLive) &&
    state.health !== "not-installed";
  // Install: same access gate as Test, but the inverse health state.
  // Only the three first-class runners have an integrations entry on
  // the agent (claude/codex/opencode → /install/<runner> wraps
  // ensureRunnerInstalledStream); ollama/aider-ollama don't.
  const canInstall =
    !!token &&
    !device.isGuest &&
    (device.online || device.workspaceLive) &&
    state.health === "not-installed" &&
    (state.id === "claude" || state.id === "codex" || state.id === "opencode");

  const base = `inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${runnerChipClass(state.health)}`;

  const runTest = useCallback(async () => {
    if (!token || inFlight.current) return;
    inFlight.current = true;
    setLocal({ kind: "running" });
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
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
      const result = await client.testRunner(state.id);
      if (result.ok) {
        setLocal({ kind: "ok", result });
        // Test just proved the runner CLI's token is valid. Broadcast
        // so the sidebar device card refetches and flips its
        // "sign in" / "auth ✓" badge accordingly — without this the
        // sidebar stayed stale until the user reloaded the page.
        broadcastPrimaryRunnerChange();
      } else if (result.needsAuth && result.supportsBrowserAuth) {
        // Auto fall-through: this is a cloud LLM that needs sign-in
        // and we have a headless flow for it. Skip the red error and
        // open the modal directly so one click = signed in.
        setLocal({ kind: "idle" });
        onSignIn(state.id);
      } else {
        setLocal({ kind: "fail", result });
      }
    } catch (err) {
      setLocal({ kind: "error", message: err instanceof Error ? err.message : String(err) });
    } finally {
      inFlight.current = false;
      try { client.disconnect(); } catch { /* nothing useful to do */ }
    }
  }, [token, device.host, device.port, device.id, device.publicEndpoints, device.tunnelUrl, state.id, onSignIn]);

  const runInstall = useCallback(async () => {
    if (!token || inFlight.current) return;
    inFlight.current = true;
    setLocal({ kind: "installing", lastLine: "" });
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
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
      // Connected directly → omit target; relay/tunnel/LAN baseUrl
      // already points at this device. Same pattern runTest above
      // uses for /agent/runners/test.
      const result = await client.installRunner(state.id, {
        onProgress: (line) => {
          // Keep the last non-empty line so the chip surfaces a tiny
          // "npm ERR! …" hint when something goes wrong without
          // blowing up the whole device card into a log viewer.
          if (line && line.trim()) {
            setLocal({ kind: "installing", lastLine: line.trim().slice(0, 80) });
          }
        },
      });
      if (result.ok) {
        setLocal({ kind: "install-ok" });
        // Refresh the runner status badges so this row flips out of
        // "not-installed" into "needs-auth" (the expected post-install
        // state). The user can then click sign-in. Same broadcast path
        // runTest uses after a successful probe.
        broadcastPrimaryRunnerChange();
      } else {
        setLocal({ kind: "install-fail", message: result.error || "install failed" });
      }
    } catch (err) {
      setLocal({
        kind: "install-fail",
        message: err instanceof Error ? err.message : String(err),
      });
    } finally {
      inFlight.current = false;
      try { client.disconnect(); } catch { /* nothing useful to do */ }
    }
  }, [token, device.host, device.port, device.id, device.publicEndpoints, device.tunnelUrl, state.id]);

  // Sign-in button kept as the primary CTA when the readiness probe
  // already says "needs auth" before we ever try a real generation.
  if (canTest && supportsBrowserAuth && state.health === "needs-auth") {
    return (
      <button
        onClick={() => onSignIn(state.id)}
        className={`${base} cursor-pointer hover:brightness-110`}
        title={`${runnerChipTitle(state)}\nClick to sign in from this browser.`}
      >
        <span className={`h-1.5 w-1.5 rounded-full ${runnerChipDotClass(state.health)}`} />
        {state.label}
        <span className="ml-0.5 text-[10px] opacity-80">· sign in</span>
      </button>
    );
  }

  // For everything else — ready, down, not-installed — show the chip
  // with a separate Test button. (We deliberately don't show Test when
  // the runner isn't installed at all; nothing to probe.)
  return (
    <span className="inline-flex items-center gap-1">
      <span className={base} title={runnerChipTitle(state)}>
        <span className={`h-1.5 w-1.5 rounded-full ${runnerChipDotClass(state.health)}`} />
        {state.label}
        {local.kind === "ok" ? (
          <span
            className="ml-1 text-[10px] text-emerald-700 dark:text-emerald-300"
            title={`Test passed in ${local.result.durationMs}ms${local.result.model ? ` (${local.result.model})` : ""}`}
          >
            ✓ {local.result.durationMs}ms
          </span>
        ) : null}
        {local.kind === "fail" ? (
          <span
            className="ml-1 text-[10px] text-red-700 dark:text-red-300"
            title={local.result.error || "test failed"}
          >
            ✗ {local.result.probe || "failed"}
          </span>
        ) : null}
        {local.kind === "error" ? (
          <span className="ml-1 text-[10px] text-red-700 dark:text-red-300" title={local.message}>
            ✗ unreachable
          </span>
        ) : null}
        {local.kind === "installing" ? (
          <span
            className="ml-1 text-[10px] text-amber-700 dark:text-amber-300"
            title={local.lastLine || "installing…"}
          >
            ⟳ installing
          </span>
        ) : null}
        {local.kind === "install-ok" ? (
          <span className="ml-1 text-[10px] text-emerald-700 dark:text-emerald-300" title="install complete — sign in next">
            ✓ installed
          </span>
        ) : null}
        {local.kind === "install-fail" ? (
          <span className="ml-1 text-[10px] text-red-700 dark:text-red-300" title={local.message}>
            ✗ install failed
          </span>
        ) : null}
      </span>
      {canInstall ? (
        <button
          onClick={runInstall}
          disabled={local.kind === "installing"}
          // Sky tint matches codex / mid-warm tone for claude. Stays
          // visually adjacent to Test so the eye keeps the same
          // landing zone whether the runner is installed or not.
          className={`rounded-md border px-1.5 py-0.5 text-[10px] font-semibold transition-colors disabled:opacity-60 ${
            local.kind === "install-ok"
              ? "border-emerald-400/60 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
              : local.kind === "install-fail"
                ? "border-red-400/60 bg-red-500/10 text-red-700 dark:text-red-200 hover:bg-red-500/20"
                : local.kind === "installing"
                  ? "border-amber-400/40 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                  : "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200 hover:border-sky-400/60 hover:text-sky-800 dark:hover:text-sky-100"
          }`}
          title={`Install ${state.label} on ${device.name || "this device"} via npm — node runtime auto-provisions if missing.`}
        >
          {local.kind === "installing" ? "…" : "Install"}
        </button>
      ) : null}
      {canTest ? (
        <button
          onClick={runTest}
          disabled={local.kind === "running"}
          // Tint matches the last result so the eye lands on the
          // runner that needs attention. Default neutral hid failures
          // when the chip itself flipped red. The runner-specific
          // accent (codex=sky, claude=violet) on idle adds enough
          // visual identity to tell the chips apart in a row.
          className={`rounded-md border px-1.5 py-0.5 text-[10px] font-semibold transition-colors disabled:opacity-60 ${
            local.kind === "ok"
              ? "border-emerald-400/60 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200 hover:bg-emerald-500/20"
              : local.kind === "fail" || local.kind === "error"
                ? "border-red-400/60 bg-red-500/10 text-red-700 dark:text-red-200 hover:bg-red-500/20"
                : local.kind === "running"
                  ? "border-amber-400/40 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                  : state.id === "codex"
                    ? "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200 hover:border-sky-400/60 hover:text-sky-800 dark:hover:text-sky-100"
                    : state.id === "claude"
                      ? "border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-200 hover:border-violet-400/60 hover:text-violet-800 dark:hover:text-violet-100"
                      : "border-surface-700 bg-surface-950/60 text-surface-300 hover:border-surface-600 hover:text-surface-100"
          }`}
          title={
            isLocalLLM
              ? `Probe local ${state.label} daemon for pass/fail`
              : `Run a quick prompt through ${state.label} on ${device.name || "this device"}`
          }
        >
          {local.kind === "running" ? "…" : "Test"}
        </button>
      ) : null}
    </span>
  );
}

function CodingAgentModal({
  device,
  token,
  signedInEmail,
  primaryRunnerByDevice,
  primaryModelByDevice,
  primaryProviderByDevice,
  liveOpenCodeByDevice,
  setPrimaryRunner,
  onSignIn,
  onClose,
}: {
  device: Device;
  token: string | null;
  signedInEmail?: string;
  primaryRunnerByDevice: Record<string, string>;
  primaryModelByDevice: Record<string, string>;
  primaryProviderByDevice: Record<string, string>;
  liveOpenCodeByDevice: Record<string, { provider?: string; model?: string } | undefined>;
  setPrimaryRunner: (
    deviceId: string,
    runnerId: string | null,
    model?: string | null,
    mode?: string | null,
    provider?: string | null,
  ) => Promise<void>;
  onSignIn: (runnerId: string) => void;
  onClose: () => void;
}) {
  const states = deriveRunnerChipStates(device);
  const explicitPrimary = primaryRunnerByDevice[device.id];
  const seededPrimary = (() => {
    if (explicitPrimary) return explicitPrimary;
    const readyIds = states.filter((s) => s.health === "ready").map((s) => s.id);
    return preferredDefaultRunnerForDevice(device, signedInEmail, readyIds);
  })();
  const primaryId = explicitPrimary ?? seededPrimary ?? "";
  const availableStates = states.filter((s) => s.health !== "not-installed");
  const availableOthers = availableStates.filter((s) => s.id !== primaryId);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="w-full max-w-3xl rounded-xl border border-slate-200 bg-white p-5 shadow-2xl dark:border-surface-700 dark:bg-surface-900">
        <div className="mb-4 flex items-start justify-between gap-3">
          <div>
            <h3 className="text-base font-semibold text-slate-900 dark:text-surface-100">Coding agent</h3>
            <p className="text-xs text-slate-500 dark:text-surface-400">
              {device.alias || device.name} · runner, model, test, and sign-in
            </p>
          </div>
          <button onClick={onClose} className="text-xl leading-none text-slate-500 hover:text-slate-900 dark:text-surface-500 dark:hover:text-surface-200">×</button>
        </div>

        <div className="rounded-lg border border-indigo-300 bg-indigo-50 px-3 py-3 dark:border-indigo-500/30 dark:bg-indigo-500/5">
          <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="flex items-center gap-1 text-[10px] font-semibold uppercase tracking-widest text-indigo-700 dark:text-indigo-300">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>
                </svg>
                Preferred
              </span>
              {primaryId ? (
                <span className="rounded border border-indigo-300 bg-white px-2 py-1 text-[11px] font-medium text-indigo-700 dark:border-indigo-500/30 dark:bg-surface-950 dark:text-indigo-100">
                  {primaryId}
                </span>
              ) : (
                <span className="text-[12px] text-slate-500 dark:text-surface-500">(none set)</span>
              )}
              {!explicitPrimary && seededPrimary ? (
                <span
                  className="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/15 dark:text-amber-300"
                  title="Suggested default based on which runners are ready on this device. Click Confirm to persist."
                >
                  suggested
                </span>
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-1.5 xl:justify-end">
              <select
                value={primaryId}
                onChange={(e) => {
                  const next = e.target.value || null;
                  const seeded = next ? preferredDefaultModelForRunner(next, device, signedInEmail) : undefined;
                  const curModel = primaryModelByDevice[device.id];
                  const prevRunner = primaryRunnerByDevice[device.id];
                  const model = next && prevRunner === next && curModel ? curModel : seeded ?? null;
                  void setPrimaryRunner(device.id, next, model).catch(() => {});
                }}
                className="rounded border border-indigo-300 bg-white px-2 py-1 text-[12px] font-medium text-indigo-700 hover:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400/40 dark:border-indigo-500/30 dark:bg-surface-900 dark:text-indigo-100 dark:hover:border-indigo-400/50"
                title="Change primary coding agent for this device. Auto-selected in every Yaver surface when this device is active."
              >
                <option value="">(none)</option>
                {availableStates.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.label}{s.health === "needs-auth" ? " · signs-in" : ""}
                  </option>
                ))}
              </select>
              {primaryId && primaryId !== "opencode" && MODEL_OPTIONS_BY_RUNNER[primaryId] ? (
                <select
                  value={primaryModelByDevice[device.id] ?? preferredDefaultModelForRunner(primaryId, device, signedInEmail) ?? ""}
                  onChange={(e) => {
                    const nextModel = e.target.value || null;
                    void setPrimaryRunner(device.id, primaryId, nextModel).catch(() => {});
                  }}
                  className="rounded border border-indigo-300 bg-white px-2 py-1 text-[11px] text-indigo-700 hover:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400/40 dark:border-indigo-500/30 dark:bg-surface-900 dark:text-indigo-100 dark:hover:border-indigo-400/50"
                  title={`Model used when spawning ${primaryId}.`}
                >
                  {MODEL_OPTIONS_BY_RUNNER[primaryId].map((m) => (
                    <option key={m.id} value={m.id} title={m.hint || ""}>
                      {m.label}
                    </option>
                  ))}
                </select>
              ) : null}
              {primaryId === "opencode" ? (() => {
                const liveCfg = liveOpenCodeByDevice[device.id];
                const savedProvider = primaryProviderByDevice[device.id] || liveCfg?.provider || "";
                const savedModelFull = primaryModelByDevice[device.id] || liveCfg?.model || "";
                const inferredProviderId = savedProvider
                  || (savedModelFull.includes("/") ? savedModelFull.split("/")[0] : "")
                  || OPENCODE_PROVIDER_CATALOGUE[0].id;
                const provider = OPENCODE_PROVIDER_CATALOGUE.find((p) => p.id === inferredProviderId)
                  || OPENCODE_PROVIDER_CATALOGUE[0];
                const inferredModelId = (() => {
                  if (!savedModelFull) return provider.models[0]?.id || "";
                  const slash = savedModelFull.indexOf("/");
                  const tail = slash >= 0 ? savedModelFull.slice(slash + 1) : savedModelFull;
                  const match = provider.models.find((m) => m.id === tail);
                  return match ? match.id : provider.models[0]?.id || "";
                })();
                return (
                  <>
                    <select
                      value={provider.id}
                      onChange={(e) => {
                        const nextProvider = OPENCODE_PROVIDER_CATALOGUE.find((p) => p.id === e.target.value);
                        if (!nextProvider) return;
                        const nextModel = nextProvider.models[0]?.id || "";
                        const fullModel = nextModel ? `${nextProvider.id}/${nextModel}` : null;
                        void setPrimaryRunner(device.id, "opencode", fullModel, null, nextProvider.id).catch(() => {});
                      }}
                      className="rounded border border-cyan-400/40 bg-white px-2 py-1 text-[11px] text-cyan-700 hover:border-cyan-400/70 focus:outline-none focus:ring-1 focus:ring-cyan-400/40 dark:border-cyan-400/30 dark:bg-surface-900 dark:text-cyan-100 dark:hover:border-cyan-400/60"
                      title="OpenCode provider for this device."
                    >
                      {OPENCODE_PROVIDER_CATALOGUE.map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.label}
                        </option>
                      ))}
                    </select>
                    {provider.models.length > 0 ? (
                      <select
                        value={inferredModelId}
                        onChange={(e) => {
                          const nextModelId = e.target.value;
                          const fullModel = nextModelId ? `${provider.id}/${nextModelId}` : null;
                          void setPrimaryRunner(device.id, "opencode", fullModel, null, provider.id).catch(() => {});
                        }}
                        className="rounded border border-fuchsia-400/40 bg-white px-2 py-1 text-[11px] text-fuchsia-700 hover:border-fuchsia-400/70 focus:outline-none focus:ring-1 focus:ring-fuchsia-400/40 dark:border-fuchsia-400/30 dark:bg-surface-900 dark:text-fuchsia-100 dark:hover:border-fuchsia-400/60"
                        title={`Model OpenCode spawns with on this device (${provider.label}).`}
                      >
                        {provider.models.map((m) => (
                          <option key={m.id} value={m.id} title={m.hint || ""}>
                            {m.label}
                          </option>
                        ))}
                      </select>
                    ) : null}
                  </>
                );
              })() : null}
              {!explicitPrimary && seededPrimary ? (
                <button
                  type="button"
                  onClick={() => {
                    const seededModel = preferredDefaultModelForRunner(seededPrimary, device, signedInEmail);
                    void setPrimaryRunner(device.id, seededPrimary, seededModel).catch(() => {});
                  }}
                  className="rounded bg-indigo-600 px-2 py-1 text-[11px] font-semibold text-white hover:bg-indigo-500 dark:bg-indigo-500 dark:hover:bg-indigo-400"
                  title="Persist this suggestion as the device's primary."
                >
                  Confirm
                </button>
              ) : null}
            </div>
          </div>
        </div>

        <div className="mt-4">
          <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-400">
            Available agents
          </div>
          <div className="flex flex-wrap items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50/70 px-3 py-3 dark:border-surface-700/70 dark:bg-[rgba(22,24,31,0.78)]">
            {availableStates.map((state) => (
              <RunnerChipWithTest
                key={`${device.id}:runner:${state.id}`}
                device={device}
                state={state}
                token={token}
                onSignIn={onSignIn}
              />
            ))}
          </div>
          {availableOthers.length > 0 ? (
            <div className="mt-2 text-[11px] text-slate-500 dark:text-surface-500">
              Other available agents ({availableOthers.length})
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
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
  /** Navigate to the dedicated Yaver Cloud page (slim summary card links here). */
  onNavigateCloud?: () => void;
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

function formatMemoryMb(value: number | undefined): string | null {
  if (typeof value !== "number" || value <= 0) return null;
  if (value >= 1024) return `${(value / 1024).toFixed(value >= 10 * 1024 ? 0 : 1)} GB`;
  return `${Math.round(value)} MB`;
}

// formatDiskUsage renders the live disk gauge as "312 / 460 GB (68%)", colouring
// the percentage once the box is genuinely under pressure. Falls back to bare
// capacity when the agent has reported specs but no live gauge yet (a box that
// heartbeats but hasn't completed its first disk scan).
function formatDiskUsage(
  storage: DeviceStorage | undefined,
  totalGbFallback: number | undefined,
): React.ReactNode | null {
  const total = storage?.totalGb ?? totalGbFallback;
  if (typeof total !== "number" || total <= 0) return null;

  const usedGb = storage?.usedGb;
  const usedPct = storage?.usedPct;
  if (typeof usedGb !== "number" || typeof usedPct !== "number") {
    return `${total.toFixed(0)} GB`;
  }

  const tone =
    usedPct >= 95 ? "text-rose-400" : usedPct >= 85 ? "text-amber-400" : "text-surface-200";
  return (
    <span>
      {usedGb.toFixed(0)} / {total.toFixed(0)} GB{" "}
      <span className={tone}>({usedPct.toFixed(0)}%)</span>
    </span>
  );
}

function formatCapabilityList(items: string[] | undefined): string | null {
  if (!Array.isArray(items)) return null;
  const cleaned = items.map((item) => String(item || "").trim()).filter(Boolean);
  return cleaned.length > 0 ? cleaned.join(", ") : null;
}

function useDevicePing(device: Device, token: string | null | undefined) {
  const [pingState, setPingState] = useState<{ pinging: boolean; rttMs?: number; ok?: boolean; error?: string; authExpired?: boolean }>({ pinging: false });

  const ping = useCallback(async () => {
    if (!token) {
      setPingState({ pinging: false, ok: false, error: "not signed in" });
      return;
    }
    // User-initiated retry clears any active backoff so the next runtime/projects
    // probe also fires immediately without waiting out the exponential delay.
    probeReset(device.id);
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
        setPingState({ pinging: false, ok: false, error: probe.error, authExpired: probe.authExpired });
      }
    } catch (e: any) {
      setPingState({ pinging: false, ok: false, error: e?.message || "probe failed" });
    }
  }, [device.host, device.id, device.port, device.tunnelUrl, device.publicEndpoints, token]);

  return { pingState, ping };
}

/**
 * Turn a failed ping into a short chip label + a fuller tooltip. The raw probe
 * error (an auth-as-same-user mismatch, a 401/403, a timeout, a genuine
 * offline box) all used to collapse to the single word "Unreachable" — which
 * told the user nothing about which of those it was. We run the error through
 * the shared connection-error classifier to get a clean, distinguishing label.
 */
function classifyPingFailure(pingState: { error?: string; authExpired?: boolean }): {
  label: string;
  title: string;
} {
  const raw = String(pingState.error || "").trim();
  const lower = raw.toLowerCase();
  // Auth-as-same-user mismatch: the agent answered but the identity differs.
  // The classifier keys off status codes, so handle this textual case here.
  if (/different user|same user|not the same|identity mismatch|wrong user|not authorized as/i.test(raw)) {
    return { label: "Not your agent", title: raw || "The agent is reachable but authenticated as a different user." };
  }
  const status =
    /\b401\b|unauthorized/.test(lower) ? 401 :
    /\b403\b|forbidden/.test(lower) ? 403 :
    undefined;
  const classified = classifyFetchError({
    error: raw ? new Error(raw) : undefined,
    response: status ? { status } : null,
    authExpired: pingState.authExpired,
  });
  // Keep the label tight enough to fit the chip; the tooltip carries the detail.
  const label =
    classified.reason === "auth-expired" ? "Auth expired" :
    classified.reason === "unauthorized" ? "Not authorized" :
    classified.reason === "forbidden" ? "Not authorized" :
    classified.reason === "timeout" ? "Timed out" :
    classified.reason === "browser-offline" ? "Offline" :
    "Unreachable";
  const title = [classified.detail, classified.suggestedAction].filter(Boolean).join(" ") ||
    raw || "Could not reach this device.";
  return { label, title };
}

/**
 * Tiny inline component that surfaces the per-device probe backoff state.
 * Without this, when a probe enters backoff the failure-reason text just
 * sits there with no indication that a retry is scheduled, and the user
 * thinks the page is frozen. Re-ticks every second to count down.
 */
/**
 * Subscribes to the module-level "last classified failure per device" registry
 * so the card-list-item can downgrade its lifecycle label the moment any
 * surface (details panel runtime probe, projects probe, future continuous
 * health probe) detects a browser-side reachability problem. Without this,
 * the card kept showing "Ready to Connect" even while DevTools filled up
 * with 502s from the very probes the details panel was running.
 */
/**
 * Card-list-item lifecycle dot + label. Pulled out of an inline IIFE so we
 * can call `useLastFailure` (a hook can't live inside a non-component IIFE).
 *
 * Downgrade conditions, in priority order:
 *   1. `device.probeState === "unreachable"` — set by other writers (mobile
 *      ping, etc.) and synced via Convex; trust it over the heartbeat-derived
 *      lifecycle.
 *   2. Any recent (<60s) classified failure in our local registry, recorded
 *      by useDeviceRuntimeInfo / useDeviceProjects. Catches the case where
 *      Convex still thinks the agent is reachable but our own /info or
 *      /projects fetches are 502'ing in the background.
 */
function DeviceLifecycleBadge({ device }: { device: Device }) {
  const lastFailure = useLastFailure(device.id);
  const lifecycle = deriveDeviceLifecycleState(device);
  const reach = deriveBrowserReach(device, lastFailure);
  const probeContradicts =
    (lifecycle === "ready-to-connect" || lifecycle === "connected") && reach.unreachable;
  const dotClass = probeContradicts
    ? "bg-warning"
    : lifecycle === "connected"
      ? "bg-success animate-live-pulse"
      : lifecycle === "bootstrap"
        ? "bg-info"
        : lifecycle === "yaver-auth-expired"
          ? "bg-warning animate-live-pulse"
          : lifecycle === "ready-to-connect"
            ? "bg-info/70"
            : "bg-surface-600";
  // The label used to read "Ready to Connect (Unauthorized)" — a contradiction
  // in one line, and the leading word is the one users act on. deviceStatusLabel
  // leads with the truth instead: "Alive · can't reach (Unauthorized)".
  const label = deviceStatusLabel(lifecycle, reach);
  const title = reach.detail
    ? `Heartbeat says the agent is alive, but our last browser probe failed: ${reach.detail}`
    : undefined;
  return (
    <>
      <span className={`inline-flex h-2 w-2 rounded-full ${dotClass}`} />
      <span
        className={`text-xs ${probeContradicts ? "text-amber-700 dark:text-amber-300" : "text-slate-500 dark:text-surface-500"}`}
        title={title}
      >
        {label}
      </span>
    </>
  );
}

/**
 * Bumps whenever ANY device's probe failure record changes. Card rendering
 * happens inside a `.map`, where per-device hooks are illegal — so the list
 * subscribes once and re-derives every card's reachability from the registry.
 */
function useFailureRegistryVersion(): number {
  const [version, setVersion] = useState(0);
  useEffect(() => subscribeLastFailure(() => setVersion((v) => v + 1)), []);
  return version;
}

function useLastFailure(deviceId: string) {
  const [snapshot, setSnapshot] = useState(() => getLastFailure(deviceId));
  useEffect(() => {
    setSnapshot(getLastFailure(deviceId));
    const unsub = subscribeLastFailure(() => setSnapshot(getLastFailure(deviceId)));
    return unsub;
  }, [deviceId]);
  return snapshot;
}

function BackoffHint({ deviceId, kind }: { deviceId: string; kind: "info" | "projects" }) {
  const [secs, setSecs] = useState(() => probeBackoffSecondsRemaining(deviceId, kind));
  useEffect(() => {
    const t = setInterval(() => setSecs(probeBackoffSecondsRemaining(deviceId, kind)), 1_000);
    return () => clearInterval(t);
  }, [deviceId, kind]);
  if (secs <= 0) return null;
  return (
    <span className="text-[10px] text-surface-500">
      Next retry in {secs}s. Click Ping above to retry now.
    </span>
  );
}

type RuntimeProbePath = "relay" | "tunnel" | "direct" | "subdomain";

interface RuntimeProbeErrorDetails {
  status?: number;
  path?: RuntimeProbePath;
  url?: string;
  message?: string;
}

function useDeviceRuntimeInfo(device: Device, enabled: boolean, token: string | null | undefined) {
  const [info, setInfo] = useState<DeviceRuntimeInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [errorDetails, setErrorDetails] = useState<RuntimeProbeErrorDetails | null>(null);
  const [loading, setLoading] = useState(false);
  // Per-candidate failure counter for backoff. Resets on success.
  const failureCountRef = useRef(0);

  useEffect(() => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) return;
    // Honour exponential backoff so a dead URL doesn't get hammered on
    // every parent re-render. Without this, the Convex device-list
    // live query (which republishes on every heartbeat) was driving
    // dozens of identical 502/404 fetches per minute against agents
    // whose tunnel was down.
    if (!probeAllowed(device.id, "info")) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setErrorDetails(null);

    // Build typed candidates so the classifier can tell relay 502s from
    // stale-subdomain CORS 404s from direct-LAN mixed-content blocks.
    type Candidate = { url: string; path: RuntimeProbePath };
    const candidates: Candidate[] = [];
    const eps = (device.publicEndpoints || []).filter(Boolean).filter(isUsablePublicEndpoint);
    const yaverEp = eps.find((e) => /^https:\/\/[^/]+\.yaver\.io(\/|$)/i.test(e));
    if (yaverEp) {
      const url = yaverEp.replace(/\/+$/, "");
      const isSub = /^https?:\/\/[0-9a-f-]{36}\.yaver\.io/i.test(url);
      candidates.push({ url, path: isSub ? "subdomain" : "tunnel" });
    }
    for (const ep of eps) {
      if (ep === yaverEp) continue;
      if (/^https:\/\//i.test(ep)) {
        const url = ep.replace(/\/+$/, "");
        const isSub = /^https?:\/\/[0-9a-f-]{36}\.yaver\.io/i.test(url);
        candidates.push({ url, path: isSub ? "subdomain" : "tunnel" });
      }
    }
    if (agentClient.activeRelayUrl && device.id) {
      candidates.push({ url: `${agentClient.activeRelayUrl}/d/${device.id}`, path: "relay" });
    }
    if (typeof window !== "undefined" && window.location.protocol !== "https:") {
      candidates.push({ url: `http://${device.host}:${device.port}`, path: "direct" });
    }
    if (candidates.length === 0) {
      setError("no reachable URL");
      setErrorDetails({ message: "no reachable URL" });
      setLoading(false);
      return;
    }
    (async () => {
      let lastErr = "no candidates";
      let lastDetails: RuntimeProbeErrorDetails | null = null;
      for (const cand of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${cand.url}/info`, {
            headers: { Authorization: `Bearer ${token}` },
            signal: AbortSignal.timeout(2_000),
          });
          if (!res.ok) {
            lastErr = `HTTP ${res.status}`;
            lastDetails = { status: res.status, path: cand.path, url: cand.url, message: lastErr };
            continue;
          }
          const data = await res.json();
          if (cancelled) return;
          setInfo(data);
          setError(null);
          setErrorDetails(null);
          failureCountRef.current = 0;
          probeSucceeded(device.id, "info");
          clearLastFailure(device.id);
          const seen = typeof data?.version === "string" ? data.version.trim() : "";
          if (seen && seen !== device.agentVersion && !device.isGuest && device.id) {
            fetch(`${CONVEX_URL}/devices/report-version`, {
              method: "POST",
              headers: {
                Authorization: `Bearer ${token}`,
                "Content-Type": "application/json",
              },
              body: JSON.stringify({ deviceId: device.id, agentVersion: seen }),
            }).catch(() => {});
          }
          setLoading(false);
          return;
        } catch (err) {
          lastErr = err instanceof Error ? err.message : "fetch failed";
          lastDetails = { path: cand.path, url: cand.url, message: lastErr };
        }
      }
      if (!cancelled) {
        setError(lastErr);
        setErrorDetails(lastDetails);
        failureCountRef.current += 1;
        probeFailed(device.id, "info", lastErr);
        const classified = classifyFetchError({
          error: lastDetails?.message ?? lastErr,
          response: lastDetails?.status ? { status: lastDetails.status } : null,
          path: lastDetails?.path,
          url: lastDetails?.url,
        });
        recordLastFailure(device.id, {
          reason: classified.reason,
          label: classified.label,
          detail: classified.detail,
        });
        setLoading(false);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive, device.agentVersion, device.isGuest]);

  return { info, error, errorDetails, loading, failureCount: failureCountRef.current };
}

interface AgentWireDevice {
  udid: string;
  name?: string;
  platform: "ios" | "android";
  os?: string;
}
interface AgentWireDevicesResponse {
  devices: AgentWireDevice[];
  count: number;
  hint?: string;
}

// useAgentWirelessDevices polls the paired agent's GET /wireless/devices
// endpoint and returns the list of WiFi-paired iPhones/iPads/Androids it
// can currently see. Mirrors the candidate-URL ordering of
// useDeviceRuntimeInfo (publicEndpoints → relay → direct LAN) so the
// dashboard never falls back to a 502-spamming direct-LAN fetch when an
// HTTPS path is available. Per the privacy contract this data lives only
// on the agent — we never persist serials or LAN IPs to Convex.
function useAgentWirelessDevices(device: Device, enabled: boolean, token: string | null | undefined) {
  const [data, setData] = useState<AgentWireDevicesResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) {
      setData(null);
      setError(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    const candidates: string[] = [];
    const eps = (device.publicEndpoints || []).filter(Boolean).filter(isUsablePublicEndpoint);
    const yaverEp = eps.find((e) => /^https:\/\/[^/]+\.yaver\.io(\/|$)/i.test(e));
    if (yaverEp) candidates.push(yaverEp);
    for (const ep of eps) {
      if (ep === yaverEp) continue;
      if (/^https:\/\//i.test(ep)) candidates.push(ep.replace(/\/+$/, ""));
    }
    if (agentClient.activeRelayUrl && device.id) {
      candidates.push(`${agentClient.activeRelayUrl}/d/${device.id}`);
    }
    if (typeof window !== "undefined" && window.location.protocol !== "https:") {
      candidates.push(`http://${device.host}:${device.port}`);
    }
    if (candidates.length === 0) {
      setError("no reachable URL");
      setLoading(false);
      return;
    }
    (async () => {
      let lastErr = "no candidates";
      for (const base of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${base}/wireless/devices`, {
            headers: { Authorization: `Bearer ${token}` },
            signal: AbortSignal.timeout(8_000),
          });
          if (!res.ok) {
            // 404 means an older agent without this endpoint — surface
            // it once instead of error-spamming.
            if (res.status === 404) {
              if (!cancelled) {
                setError("agent does not yet expose /wireless/devices (update the agent on this machine)");
                setLoading(false);
              }
              return;
            }
            lastErr = `HTTP ${res.status}`;
            continue;
          }
          const body = (await res.json()) as AgentWireDevicesResponse;
          if (cancelled) return;
          setData(body);
          setError(null);
          setLoading(false);
          return;
        } catch (err) {
          lastErr = err instanceof Error ? err.message : "fetch failed";
        }
      }
      if (!cancelled) {
        setError(lastErr);
        setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive]);

  return { data, error, loading };
}

function useDeviceAgentUpdate(device: Device, enabled: boolean, token: string | null | undefined) {
  const [status, setStatus] = useState<AgentUpdateStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [updating, setUpdating] = useState(false);

  const connectClient = useCallback(async () => {
    if (!token) throw new Error("not signed in");
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
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
    return client;
  }, [token, device.host, device.port, device.id, device.publicEndpoints, device.tunnelUrl]);

  const refresh = useCallback(async () => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) return;
    setLoading(true);
    setError(null);
    let client: AgentClient | null = null;
    try {
      client = await connectClient();
      const next = await client.getAgentUpdateStatus();
      setStatus(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "failed to fetch update status");
    } finally {
      setLoading(false);
      try { client?.disconnect(); } catch {}
    }
  }, [enabled, token, device.online, device.workspaceLive, connectClient]);

  const trigger = useCallback(async () => {
    if (!token) throw new Error("not signed in");
    setUpdating(true);
    setError(null);
    let client: AgentClient | null = null;
    try {
      client = await connectClient();
      const res = await client.triggerAgentUpdate();
      await refresh();
      return res;
    } catch (err) {
      const msg = err instanceof Error ? err.message : "failed to trigger update";
      setError(msg);
      throw err;
    } finally {
      setUpdating(false);
      try { client?.disconnect(); } catch {}
    }
  }, [token, connectClient, refresh]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { status, error, loading, updating, refresh, trigger };
}

interface DeviceProjectInfo {
  name: string;
  path?: string;
  branch?: string;
  framework?: string;
  tags?: string[];
  // Extended fields surfaced on the device-card project chip rail.
  // The agent's /projects endpoint already returns these; the hook
  // mapper just needs to forward them.
  remote?: string;        // git remote URL ("origin"); empty = no git
  monorepoRoot?: string;  // path to repo root if this project is one
                          // app inside a monorepo (yaver.workspace.yaml)
  monorepoApp?: string;   // app name within the monorepo
}

/**
 * Tracks agentClient.connectionState as React state so consuming
 * components re-run when the dashboard's active workspace flips
 * between disconnected → connecting → connected. Otherwise hooks
 * that branch on agentClient.connectionState would only see the
 * stale value captured at their first render — which is exactly
 * why the folded Git-projects rail kept saying "unavailable" even
 * after Open Workspace finished: the device.workspaceLive registry
 * flag flipped before agentClient.connectionState did, so the
 * useEffect re-ran while the client was still "connecting" and the
 * agentClient.listProjectsByCapability path threw assertConnected.
 */
function useAgentConnectionState(): string {
  const [state, setState] = useState<string>(() => agentClient.connectionState);
  useEffect(() => {
    const unsubscribe = agentClient.on("connectionState", (s) => setState(s));
    // Sync once in case state changed between render + subscribe.
    setState(agentClient.connectionState);
    return unsubscribe;
  }, []);
  return state;
}

function useDeviceProjects(device: Device, enabled: boolean, token: string | null | undefined) {
  const [projects, setProjects] = useState<DeviceProjectInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [errorDetails, setErrorDetails] = useState<{
    status?: number;
    path?: "relay" | "tunnel" | "direct" | "subdomain";
    url?: string;
    message?: string;
  } | null>(null);
  const [loading, setLoading] = useState(false);
  const agentConnectionState = useAgentConnectionState();

  useEffect(() => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) return;
    if (!probeAllowed(device.id, "projects")) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setErrorDetails(null);

    // Same probe-ordering rules as useDeviceRuntimeInfo: prefer
    // HTTPS (relay path or *.yaver.io subdomain), only fall through
    // to direct LAN when the dashboard is on http (local dev). On
    // https://yaver.io, fetching http://<lan>/projects gets blocked
    // and we end up with a misleading "fetch failed" error.
    type Candidate = { url: string; path: "relay" | "tunnel" | "direct" | "subdomain" };
    const candidates: Candidate[] = [];
    if (agentClient.activeRelayUrl && device.id) {
      candidates.push({ url: `${agentClient.activeRelayUrl}/d/${device.id}`, path: "relay" });
    }
    const eps = (device.publicEndpoints || []).filter(Boolean).filter(isUsablePublicEndpoint);
    for (const ep of eps) {
      if (/^https:\/\//i.test(ep)) {
        const url = ep.replace(/\/+$/, "");
        // {deviceId}.yaver.io subdomains aren't wired to the relay yet —
        // they 404 + CORS-block. Tag them so the classifier can produce
        // an actionable "stale subdomain" reason instead of generic
        // "network error".
        const isSubdomain = /^https?:\/\/[0-9a-f-]{36}\.yaver\.io/i.test(url);
        candidates.push({ url, path: isSubdomain ? "subdomain" : "tunnel" });
      }
    }
    if (typeof window !== "undefined" && window.location.protocol !== "https:") {
      candidates.push({ url: `http://${device.host}:${device.port}`, path: "direct" });
    }

    // If the device is the dashboard's currently-active workspace, the
    // most reliable transport is `agentClient` itself — same baseUrl
    // + authHeaders that already serve /info, /tasks, /agent/runners
    // etc. Hand-rolled relay URL + raw Bearer token returns 401
    // because the relay-side auth contract differs from the agent's
    // session-token contract. Try agentClient first, fall back to
    // candidate URLs if it errors so non-active rows still surface
    // their projects through the registry-backed path.
    const activeRelayUrl = agentClient.activeRelayUrl ?? null;
    const isActiveDevice =
      agentConnectionState === "connected" &&
      (Boolean(activeRelayUrl && activeRelayUrl.includes(`/d/${device.id}`)) ||
        !!device.workspaceLive);

    const mapAgentRow = (p: any): DeviceProjectInfo => ({
      name: String(p?.name ?? p?.slug ?? "").trim(),
      path: typeof p?.path === "string" ? p.path : undefined,
      branch: typeof p?.branch === "string" ? p.branch : undefined,
      framework: typeof p?.framework === "string" ? p.framework : undefined,
      tags: Array.isArray(p?.tags) ? p.tags.map(String) : undefined,
      remote: typeof p?.remote === "string" && p.remote.trim() ? p.remote : undefined,
      monorepoRoot:
        typeof p?.monorepoRoot === "string" && p.monorepoRoot.trim() ? p.monorepoRoot : undefined,
      monorepoApp:
        typeof p?.monorepoApp === "string" && p.monorepoApp.trim() ? p.monorepoApp : undefined,
    });

    (async () => {
      // Live-workspace happy path.
      if (isActiveDevice) {
        try {
          const list = await agentClient.listProjectsByCapability("all");
          if (cancelled) return;
          const mapped = (list || []).map(mapAgentRow).filter((p) => p.name.length > 0);
          setProjects(mapped);
          setError(null);
          setLoading(false);
          return;
        } catch (err) {
          // Fall through — try candidate URLs too. We still want the
          // "Load failed" string captured in case all paths fail.
          // (Don't surface this fall-through error directly; the
          // candidates probe gets the last word.)
        }
      }

      if (candidates.length === 0) {
        if (!cancelled) {
          setError("no reachable URL");
          setErrorDetails({ message: "no reachable URL" });
          setLoading(false);
        }
        return;
      }

      let lastErr = "no candidates";
      let lastDetails: typeof errorDetails = null;
      for (const cand of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${cand.url}/projects`, {
            headers: { Authorization: `Bearer ${token}` },
            signal: AbortSignal.timeout(3_000),
          });
          if (!res.ok) {
            lastErr = `HTTP ${res.status}`;
            lastDetails = { status: res.status, path: cand.path, url: cand.url, message: lastErr };
            continue;
          }
          const data = await res.json();
          const arr: any[] = Array.isArray(data) ? data : Array.isArray(data?.projects) ? data.projects : [];
          const mapped: DeviceProjectInfo[] = arr.map(mapAgentRow).filter((p: DeviceProjectInfo) => p.name.length > 0);
          if (cancelled) return;
          setProjects(mapped);
          setError(null);
          setErrorDetails(null);
          probeSucceeded(device.id, "projects");
          // Don't clearLastFailure here — /info is the primary reachability
          // signal. If /projects works but /info still failed, the failure
          // record should remain.
          setLoading(false);
          return;
        } catch (err) {
          lastErr = err instanceof Error ? err.message : "fetch failed";
          lastDetails = { path: cand.path, url: cand.url, message: lastErr };
        }
      }
      if (!cancelled) {
        setError(lastErr);
        setErrorDetails(lastDetails);
        probeFailed(device.id, "projects", lastErr);
        const classified = classifyFetchError({
          error: lastDetails?.message ?? lastErr,
          response: lastDetails?.status ? { status: lastDetails.status } : null,
          path: lastDetails?.path,
          url: lastDetails?.url,
        });
        recordLastFailure(device.id, {
          reason: classified.reason,
          label: classified.label,
          detail: classified.detail,
        });
        setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive, agentConnectionState]);

  return { projects, error, errorDetails, loading };
}

/**
 * Loads the user's current primary + secondary device IDs from Convex
 * and exposes setters that POST back to /settings. Shared between the
 * dashboard's device cards so only one settings round-trip is made on
 * mount. Null state ("no elevated device") is the default.
 */
function usePrimaryDeviceId(token: string | null | undefined): {
  primaryDeviceId: string | null;
  setPrimaryDevice: (id: string | null) => Promise<void>;
  secondaryDeviceId: string | null;
  setSecondaryDevice: (id: string | null) => Promise<void>;
} {
  const [primaryDeviceId, setPrimaryDeviceId] = useState<string | null>(null);
  const [secondaryDeviceId, setSecondaryDeviceId] = useState<string | null>(null);

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
          setSecondaryDeviceId(data?.settings?.secondaryDeviceId ?? null);
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

  const setSecondaryDevice = useCallback(async (id: string | null) => {
    if (!token) return;
    const previous = secondaryDeviceId;
    setSecondaryDeviceId(id);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ secondaryDeviceId: id }),
      });
      if (!res.ok) throw new Error(`status ${res.status}`);
    } catch (e) {
      setSecondaryDeviceId(previous);
      throw e;
    }
  }, [token, secondaryDeviceId]);

  return { primaryDeviceId, setPrimaryDevice, secondaryDeviceId, setSecondaryDevice };
}

/**
 * Latest GitHub release version of the Go agent. Cached in
 * localStorage with a 1h TTL to avoid hammering the GitHub API
 * (60 unauthenticated requests/hour limit) when the user opens the
 * Devices tab repeatedly. Returns null while loading or if the API
 * is unreachable — callers fall back to "no update banner".
 */
export function useLatestAgentVersion(): string | null {
  const [latest, setLatest] = useState<string | null>(null);

  useEffect(() => {
    const cacheKey = "yaver_latest_agent_version";
    const cacheTtlMs = 60 * 60 * 1000; // 1h
    try {
      const raw = typeof window !== "undefined" ? window.localStorage.getItem(cacheKey) : null;
      if (raw) {
        const parsed = JSON.parse(raw) as { version: string; fetchedAt: number };
        if (parsed.version && Date.now() - parsed.fetchedAt < cacheTtlMs) {
          setLatest(parsed.version);
          return;
        }
      }
    } catch { /* ignore parse errors */ }

    let cancelled = false;
    (async () => {
      try {
        const res = await fetch("https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest");
        if (!res.ok) return;
        const data = await res.json();
        const tag = String(data?.tag_name || "").replace(/^v/, "");
        if (!tag) return;
        if (!cancelled) setLatest(tag);
        try {
          window.localStorage.setItem(cacheKey, JSON.stringify({ version: tag, fetchedAt: Date.now() }));
        } catch { /* private mode / quota */ }
      } catch { /* network error */ }
    })();
    return () => { cancelled = true; };
  }, []);

  return latest;
}

/** Compare two semver-ish "1.99.49" strings. +1 a > b, 0 equal, -1 a < b. */
export function compareSemver(a: string, b: string): number {
  const pa = a.split(".").map((n) => parseInt(n, 10) || 0);
  const pb = b.split(".").map((n) => parseInt(n, 10) || 0);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const x = pa[i] || 0;
    const y = pb[i] || 0;
    if (x > y) return 1;
    if (x < y) return -1;
  }
  return 0;
}

/**
 * Per-device primary runner: lets the user say "on this machine, default
 * to codex" while keeping a different default on another machine. Stored
 * in userSettings.primaryRunnerByDevice on Convex; we keep a flat map
 * here for fast lookup. The user-visible flow is the small dropdown on
 * each device card.
 */
// Re-exported so the dashboard can read the same map without
// duplicating the Convex round-trip. Hooks used in two trees still
// fire two fetches, but they use Convex's HTTP cache so it's cheap;
// long-term we should hoist this to a shared context.
// Custom event broadcast across all usePrimaryRunnerByDevice
// instances so sidebar + Devices tab + Chat tab all refetch
// whenever any one of them saves a new primary runner. Without this
// the sidebar device card kept showing stale "Claude Code" after
// the user picked Codex from the Devices tab — each hook instance
// had its own state map and never observed the other's optimistic
// update.
const PRIMARY_RUNNER_EVENT = "yaver:primary-runner-changed";
function broadcastPrimaryRunnerChange() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(PRIMARY_RUNNER_EVENT));
}

export function usePrimaryRunnerByDevice(token: string | null | undefined): {
  primaryRunnerByDevice: Record<string, string>;
  /** Per-device model hint (optional) — `claude-opus-4-7`, `gpt-5-codex`,
   *  `qwen2.5-coder:14b`, … — read from the same Convex row and stored
   *  alongside runnerId. Empty when the user hasn't picked one yet. */
  primaryModelByDevice: Record<string, string>;
  primaryModeByDevice: Record<string, string>;
  primaryProviderByDevice: Record<string, string>;
  setPrimaryRunner: (
    deviceId: string,
    runnerId: string | null,
    model?: string | null,
    mode?: string | null,
    provider?: string | null,
  ) => Promise<void>;
} {
  const [runnerMap, setRunnerMap] = useState<Record<string, string>>({});
  const [modelMap, setModelMap] = useState<Record<string, string>>({});
  const [modeMap, setModeMap] = useState<Record<string, string>>({});
  const [providerMap, setProviderMap] = useState<Record<string, string>>({});
  const [refreshNonce, setRefreshNonce] = useState(0);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const onChange = () => setRefreshNonce((n) => n + 1);
    window.addEventListener(PRIMARY_RUNNER_EVENT, onChange);
    return () => window.removeEventListener(PRIMARY_RUNNER_EVENT, onChange);
  }, []);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    (async () => {
      try {
        // Bypass any HTTP cache — without no-store the broadcast
        // event can fire and the refetch returns the previous map
        // because the browser already had a fresh copy in cache.
        // That's how the sidebar kept showing "Claude Code" after
        // the user picked Codex.
        const res = await fetch(`${CONVEX_URL}/settings`, {
          headers: { Authorization: `Bearer ${token}` },
          cache: "no-store",
        });
        if (!res.ok) return;
        const data = await res.json();
        const rows = Array.isArray(data?.settings?.primaryRunnerByDevice)
          ? (data.settings.primaryRunnerByDevice as Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }>)
          : [];
        if (!cancelled) {
          const runners: Record<string, string> = {};
          const models: Record<string, string> = {};
          const modes: Record<string, string> = {};
          const providers: Record<string, string> = {};
          for (const row of rows) {
            if (!row?.deviceId || !row?.runnerId) continue;
            runners[row.deviceId] = row.runnerId;
            if (row.model) models[row.deviceId] = row.model;
            if (row.mode) modes[row.deviceId] = row.mode;
            if (row.provider) providers[row.deviceId] = row.provider;
          }
          setRunnerMap(runners);
          setModelMap(models);
          setModeMap(modes);
          setProviderMap(providers);
        }
      } catch {
        // best-effort — falls back to no per-device pref
      }
    })();
    return () => { cancelled = true; };
  }, [token, refreshNonce]);

  const setPrimaryRunner = useCallback(
    async (deviceId: string, runnerId: string | null, model?: string | null, mode?: string | null, provider?: string | null) => {
      if (!token) return;
      const previousRunner = runnerMap;
      const previousModel = modelMap;
      const previousMode = modeMap;
      const previousProvider = providerMap;
      // Optimistic update.
      setRunnerMap((prev) => {
        const next = { ...prev };
        if (runnerId) next[deviceId] = runnerId;
        else delete next[deviceId];
        return next;
      });
      setModelMap((prev) => {
        const next = { ...prev };
        if (!runnerId || model === null) {
          delete next[deviceId];
        } else if (typeof model === "string" && model.length > 0) {
          next[deviceId] = model;
        }
        return next;
      });
      setModeMap((prev) => {
        const next = { ...prev };
        if (!runnerId || mode === null) {
          delete next[deviceId];
        } else if (typeof mode === "string" && mode.length > 0) {
          next[deviceId] = mode;
        }
        return next;
      });
      setProviderMap((prev) => {
        const next = { ...prev };
        if (!runnerId || provider === null) {
          delete next[deviceId];
        } else if (typeof provider === "string" && provider.length > 0) {
          next[deviceId] = provider;
        }
        return next;
      });
      try {
        const body: Record<string, unknown> = {
          primaryRunnerForDevice: {
            deviceId,
            runnerId,
            ...(model !== undefined ? { model } : {}),
            ...(mode !== undefined ? { mode } : {}),
            ...(provider !== undefined ? { provider } : {}),
          },
        };
        const res = await fetch(`${CONVEX_URL}/settings`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (!res.ok) throw new Error(`status ${res.status}`);
        // Tell every other hook instance (sidebar, Chat tab, Webview)
        // to refetch so they show the new primary runner immediately.
        broadcastPrimaryRunnerChange();
      } catch (e) {
        setRunnerMap(previousRunner);
        setModelMap(previousModel);
        setModeMap(previousMode);
        setProviderMap(previousProvider);
        throw e;
      }
    },
    [token, runnerMap, modelMap, modeMap, providerMap],
  );

  return {
    primaryRunnerByDevice: runnerMap,
    primaryModelByDevice: modelMap,
    primaryModeByDevice: modeMap,
    primaryProviderByDevice: providerMap,
    setPrimaryRunner,
  };
}

/**
 * For each device whose Convex row says runnerId="opencode" but has no
 * provider/model recorded, fetch the live opencode.json over the relay
 * and surface its `model` field (e.g. "zai/glm-4.7") so the dropdowns
 * can display the user's actual config instead of falling back to
 * OPENCODE_PROVIDER_CATALOGUE[0] (Anthropic / Sonnet 4.6).
 *
 * Half-populated Convex rows happen when a user taps the "opencode"
 * default-runner pill on mobile without going through OpenCodeConfigModal,
 * which writes only `runnerId` and (worse) clears any prior model.
 * Mobile is being patched in parallel; this hook covers existing rows.
 */
function useLiveOpenCodeByDevice(
  devices: Device[],
  runnerByDevice: Record<string, string>,
  providerByDevice: Record<string, string>,
  modelByDevice: Record<string, string>,
  agentConnected: boolean,
): Record<string, { provider: string; model: string }> {
  const [live, setLive] = useState<Record<string, { provider: string; model: string }>>({});
  const fetchedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!agentConnected) return;
    let cancelled = false;
    (async () => {
      for (const d of devices) {
        if (runnerByDevice[d.id] !== "opencode") continue;
        if (providerByDevice[d.id] || modelByDevice[d.id]) continue;
        if (fetchedRef.current.has(d.id)) continue;
        fetchedRef.current.add(d.id);
        try {
          const cfg = await agentClient.openCodeConfig(d.id);
          if (cancelled) return;
          const m = (cfg?.model || "").trim();
          if (!m) continue;
          const slash = m.indexOf("/");
          const provider = slash > 0 ? m.slice(0, slash) : "";
          setLive((prev) => ({ ...prev, [d.id]: { provider, model: m } }));
        } catch {
          // Device unreachable / opencode not installed — leave the
          // catalogue fallback in place. Allow a retry on next change.
          fetchedRef.current.delete(d.id);
        }
      }
    })();
    return () => { cancelled = true; };
  }, [devices, runnerByDevice, providerByDevice, modelByDevice, agentConnected]);

  return live;
}

// Default model per runner when the user hasn't picked one yet.
// Applied when the user selects a primary runner and has no prior
// model choice, so `claude` seeds `opus-4-7` (user's explicit ask
// for "latest opus"), `codex` seeds `gpt-5.4` (the current GPT-5
// release; older intermediates `o3-mini` and `gpt-5-codex` are
// migrated away in mobile DeviceContext.loadSettings).
export const DEFAULT_MODEL_BY_RUNNER: Record<string, string> = {
  claude: "claude-opus-4-7",
  // Codex-native model — general gpt-5.x require API billing and error on a
  // ChatGPT-account Codex login ("not supported when using Codex with a
  // ChatGPT account").
  codex: "gpt-5.3-codex",
};

export function isKivancAccount(email: string | null | undefined): boolean {
  const normalized = String(email || "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.NEXT_PUBLIC_YAVER_OWNER_EMAIL ||
    process.env.NEXT_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    "";
  const allowed = raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}

export function isKivancMacBook(device: Pick<Device, "name" | "hostName" | "platform">): boolean {
  const haystack = `${device.name || ""} ${device.hostName || ""}`.toLowerCase();
  const isMac = ["darwin", "macos"].includes(String(device.platform || "").trim().toLowerCase());
  if (!isMac) return false;
  return haystack.includes("kivanc") || haystack.includes("cakmak") || haystack.includes("macbook");
}

export function preferredDefaultRunnerForDevice(
  device: Pick<Device, "name" | "hostName" | "platform">,
  signedInEmail: string | null | undefined,
  availableRunnerIds: string[],
): string | null {
  if (availableRunnerIds.length === 0) return null;
  if (isKivancAccount(signedInEmail)) {
    if (isKivancMacBook(device) && availableRunnerIds.includes("claude")) {
      return "claude";
    }
    if (!isKivancMacBook(device) && availableRunnerIds.includes("codex")) {
      return "codex";
    }
  }
  if (availableRunnerIds.includes("claude")) return "claude";
  if (availableRunnerIds.includes("codex")) return "codex";
  return availableRunnerIds[0] || null;
}

export function preferredDefaultModelForRunner(
  runnerId: string | null | undefined,
  device: Pick<Device, "name" | "hostName" | "platform">,
  signedInEmail: string | null | undefined,
): string | null {
  const normalized = String(runnerId || "").trim().toLowerCase();
  if (!normalized) return null;
  if (isKivancAccount(signedInEmail)) {
    if (normalized === "claude" && isKivancMacBook(device)) {
      return "claude-opus-4-7";
    }
    if (normalized === "codex" && !isKivancMacBook(device)) {
      return "gpt-5.3-codex";
    }
  }
  return DEFAULT_MODEL_BY_RUNNER[normalized] || null;
}

// First-class runners surfaced in the chat / start-task pickers across
// web + mobile. Aider / Ollama / Aider+Qwen are still installable and
// callable from the CLI + MCP — they just don't show up in the
// consumer UIs. Local Ollama is reachable through OpenCode as a
// provider.
export const RUNNER_WHITELIST = ["claude", "codex", "opencode"] as const;
export const RUNNER_WHITELIST_SET: ReadonlySet<string> = new Set(RUNNER_WHITELIST);

// OpenCode provider catalogue — what the user picks when they choose
// the "OpenCode" runner. Each provider lists a handful of well-known
// coding models. Selecting any model from a `requiresKey: true`
// provider triggers an inline API-key prompt; Ollama is keyless.
export type OpenCodeCatalogueModel = {
  id: string;            // model id forwarded to OpenCode (no provider prefix)
  label: string;
  hint?: string;
};
export type OpenCodeCatalogueProvider = {
  id: string;            // matches opencode.json provider key
  label: string;
  baseUrl?: string;      // default base URL written into opencode.json
  requiresKey: boolean;
  keyEnv?: string;       // env-var hint shown next to the input
  blurb: string;         // one-liner shown under the provider chip
  models: OpenCodeCatalogueModel[];
};
export const OPENCODE_PROVIDER_CATALOGUE: OpenCodeCatalogueProvider[] = [
  {
    id: "anthropic",
    label: "Anthropic",
    requiresKey: true,
    keyEnv: "ANTHROPIC_API_KEY",
    blurb: "Bring your own Anthropic key. Highest quality.",
    models: [
      { id: "claude-sonnet-4-6", label: "Sonnet 4.6", hint: "balanced default" },
      { id: "claude-opus-4-7", label: "Opus 4.7", hint: "highest quality, ~5× cost" },
      { id: "claude-haiku-4-5", label: "Haiku 4.5", hint: "fastest, cheapest" },
    ],
  },
  {
    id: "openai",
    label: "OpenAI",
    requiresKey: true,
    keyEnv: "OPENAI_API_KEY",
    blurb: "GPT-5 family via your OpenAI key.",
    models: [
      { id: "gpt-5.4", label: "GPT-5.4", hint: "current default" },
      { id: "gpt-5-codex", label: "GPT-5 Codex", hint: "agentic coding" },
      { id: "gpt-5", label: "GPT-5", hint: "general reasoning" },
      { id: "gpt-5-mini", label: "GPT-5 Mini", hint: "fast + cheap" },
    ],
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    baseUrl: "https://openrouter.ai/api/v1",
    requiresKey: true,
    keyEnv: "OPENROUTER_API_KEY",
    blurb: "One key, hundreds of models. Good for trying things.",
    models: [
      { id: "anthropic/claude-sonnet-4.6", label: "Claude Sonnet 4.6" },
      { id: "openai/gpt-5", label: "GPT-5" },
      { id: "deepseek/deepseek-chat", label: "DeepSeek V3" },
      { id: "qwen/qwen-2.5-coder-32b-instruct", label: "Qwen Coder 32B" },
      { id: "meta-llama/llama-3.3-70b-instruct", label: "Llama 3.3 70B" },
    ],
  },
  {
    id: "zai-coding-plan",
    label: "GLM 4.7 Coding Plan",
    requiresKey: true,
    keyEnv: "ZAI_API_KEY",
    blurb: "z.ai Coding Plan via OpenCode's built-in provider. Key from z.ai (separate from Zhipu OpenAPI keys).",
    models: [
      { id: "glm-4.7", label: "GLM-4.7", hint: "newest, coding-tuned" },
      { id: "glm-4.5-air", label: "GLM-4.5 Air", hint: "lighter, faster" },
    ],
  },
  {
    id: "glm",
    label: "Zhipu GLM",
    baseUrl: "https://open.bigmodel.cn/api/paas/v4",
    requiresKey: true,
    keyEnv: "GLM_API_KEY",
    blurb: "Zhipu OpenAPI / bigmodel.cn. Separate key from z.ai Coding Plan.",
    models: [
      { id: "glm-4.5-air", label: "GLM-4.5 Air", hint: "fast + cheap" },
      { id: "glm-4.5", label: "GLM-4.5", hint: "general coding" },
      { id: "glm-4-plus", label: "GLM-4 Plus", hint: "legacy larger model" },
    ],
  },
  {
    id: "groq",
    label: "Groq",
    baseUrl: "https://api.groq.com/openai/v1",
    requiresKey: true,
    keyEnv: "GROQ_API_KEY",
    blurb: "Fast hosted open-weight models via Groq.",
    models: [
      { id: "qwen/qwen3-32b", label: "Qwen3 32B" },
      { id: "llama-3.3-70b-versatile", label: "Llama 3.3 70B" },
      { id: "deepseek-r1-distill-llama-70b", label: "DeepSeek R1 Distill 70B" },
    ],
  },
  {
    id: "together",
    label: "Together",
    baseUrl: "https://api.together.xyz/v1",
    requiresKey: true,
    keyEnv: "TOGETHER_API_KEY",
    blurb: "Hosted open-weight coding models via Together AI.",
    models: [
      { id: "Qwen/Qwen2.5-Coder-32B-Instruct", label: "Qwen Coder 32B" },
      { id: "deepseek-ai/DeepSeek-V3", label: "DeepSeek V3" },
      { id: "meta-llama/Llama-3.3-70B-Instruct-Turbo", label: "Llama 3.3 70B" },
    ],
  },
  {
    id: "deepseek",
    label: "DeepSeek",
    baseUrl: "https://api.deepseek.com",
    requiresKey: true,
    keyEnv: "DEEPSEEK_API_KEY",
    blurb: "DeepSeek-hosted coding/reasoning models.",
    models: [
      { id: "deepseek-chat", label: "DeepSeek Chat" },
      { id: "deepseek-reasoner", label: "DeepSeek Reasoner" },
    ],
  },
  {
    id: "ollama",
    label: "Ollama (local, free)",
    baseUrl: "http://127.0.0.1:11434/v1",
    requiresKey: false,
    blurb: "Runs entirely on this machine. No keys, no spend.",
    models: [
      { id: "qwen2.5-coder:14b", label: "Qwen Coder 14B", hint: "fits 24 GB RAM" },
      { id: "qwen2.5-coder:7b", label: "Qwen Coder 7B", hint: "fits 16 GB RAM" },
      { id: "qwen2.5-coder:32b", label: "Qwen Coder 32B", hint: "needs 48+ GB" },
      { id: "deepseek-coder-v2:16b", label: "DeepSeek Coder v2 16B" },
      { id: "llama3.3:70b", label: "Llama 3.3 70B", hint: "needs 64+ GB" },
    ],
  },
  {
    id: "ollama-tailscale",
    label: "Ollama (Tailscale)",
    baseUrl: "http://yaver-gpu.tailscale.net:11434/v1",
    requiresKey: false,
    blurb: "Remote Ollama over your tailnet. Edit the host in settings if needed.",
    models: [
      { id: "qwen2.5-coder:14b", label: "Qwen Coder 14B" },
      { id: "qwen2.5-coder:32b", label: "Qwen Coder 32B" },
      { id: "deepseek-coder-v2:16b", label: "DeepSeek Coder v2 16B" },
    ],
  },
];

// Options shown in the per-runner model dropdown. First entry is the
// default. Full model ids so the agent can forward them verbatim to
// `--model` / YAVER_CLAUDE_MODEL / YAVER_CODEX_MODEL. Only real model
// identifiers — anything the runner's CLI would actually accept.
export const MODEL_OPTIONS_BY_RUNNER: Record<string, Array<{ id: string; label: string; hint?: string }>> = {
  claude: [
    { id: "claude-opus-4-7", label: "Opus 4.7", hint: "highest quality, ~5× Sonnet cost" },
    { id: "claude-opus-4-6", label: "Opus 4.6", hint: "prior Opus" },
    { id: "claude-sonnet-4-6", label: "Sonnet 4.6", hint: "daily work, balanced" },
    { id: "claude-sonnet-4-5", label: "Sonnet 4.5", hint: "prior Sonnet" },
    { id: "claude-haiku-4-5", label: "Haiku 4.5", hint: "fastest, cheapest" },
  ],
  codex: [
    { id: "gpt-5.4", label: "GPT-5.4", hint: "stable default fallback" },
    { id: "gpt-5-codex", label: "GPT-5 Codex", hint: "agentic coding model" },
    { id: "gpt-5-thinking", label: "GPT-5 Thinking", hint: "reasoning-heavy" },
    { id: "gpt-5", label: "GPT-5", hint: "general reasoning" },
    { id: "gpt-5-mini", label: "GPT-5 Mini", hint: "fastest, cheapest" },
    { id: "o3", label: "o3", hint: "prior reasoning line" },
  ],
};

// Managed-cloud provenance. Every `cloudMachines` row is a Yaver-side
// box (origin "managed" — see backend/convex/cloudMachines.ts). We
// fetch the user's managed-machine list once and key it by the agent
// deviceId so each device card can label itself "Yaver Cloud" vs
// "Self-hosted". Purely informational; the entitlement gate is always
// server-side. A failed fetch just falls back to "Self-hosted".
function useManagedDeviceIds(token: string | null | undefined) {
  const [ids, setIds] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    void (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/subscription`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok) return;
        const data = await res.json().catch(() => ({}));
        const next = new Set<string>(
          (Array.isArray(data?.machines) ? data.machines : [])
            .map((m: { deviceId?: unknown }) =>
              typeof m?.deviceId === "string" ? m.deviceId : null,
            )
            .filter(Boolean) as string[],
        );
        if (!cancelled) setIds(next);
      } catch {
        /* non-fatal — badge falls back to self-hosted */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);
  return ids;
}

// Provisioning-phase → human label. MUST stay in sync with the same
// map in ManagedCloudPanel.tsx (single source would be nicer but that
// file is co-owned by a parallel session; a 9-entry literal is the
// lower-risk dup). Keyed by cloudMachines.provisionPhase.
const PROVISION_PHASE_LABEL: Record<string, string> = {
  creating: "Reserving your box…",
  booting: "Booting & installing Docker…",
  "installing-docker": "Installing Docker…",
  "pulling-image": "Pulling the Yaver image…",
  "starting-agent": "Starting the Yaver agent…",
  registering: "Registering your device…",
  "authorizing-runners": "Almost there — finishing setup…",
  // Not progress. The box is up, but its Yaver session expired and it cannot
  // register on its own, so nothing here changes until the user acts. Without
  // an entry this rendered the raw slug "awaiting-yaver-auth" under a moving
  // progress bar promising the card would appear "automatically" — which it
  // never would.
  "awaiting-yaver-auth": "Waiting for you to sign this box in",
  ready: "Ready",
  error: "Setup failed",
};

/**
 * A box that is awake but blocked on the user is neither "setting up" nor
 * "failed" — it is waiting, and it will wait forever unless someone signs it
 * in. Rendering it as either one is a lie: the progress branch promises it
 * resolves itself, the failure branch tells the user to delete and re-buy a
 * box that is perfectly fine.
 */
function isAwaitingYaverAuth(m: { provisionPhase?: string | null }): boolean {
  return m.provisionPhase === "awaiting-yaver-auth";
}

export interface ManagedMachineSummary {
  id: string;
  machineType: string;
  status: string;
  hostname: string | null;
  serverIp: string | null;
  region: string | null;
  deviceId: string | null;
  provisionPhase: string | null;
  provisionProgress: number | null;
  provisionError: string | null;
  /**
   * The control plane's own sentence about why this box is stuck — e.g. "The
   * box is awake but its Yaver agent session expired. Sign this machine in
   * from your phone to finish wake."
   *
   * /subscription has always sent it; web simply never modelled it, so the
   * dashboard had nothing to show and fell back to a progress bar promising
   * the box would connect "automatically". It could not.
   */
  errorMessage: string | null;
  /** When the CURRENT phase began — the anchor for the in-phase timer. */
  provisionPhaseAt: number | null;
  /** When this wake was requested — the anchor for the total wake clock. */
  lastWokeAt: number | null;
  /** Provider's own word for the server state during a wake. */
  providerStatus: string | null;
  providerStatusAt: number | null;
  /** Measured on THIS box — drives a real ETA instead of a constant. */
  lastWakeDurationMs: number | null;
  /** How the last wake ended, so a parked box can explain itself. */
  lastWakeOutcome: string | null;
  lastParkedAt: number | null;
  snapshotSizeGb: number | null;
  snapshotCreatedAt: number | null;
  hasVolume: boolean;
  runnersAuthorized: boolean;
}

// Full managed-machine list — the same /subscription payload
// useManagedDeviceIds reads, kept as a separate hook so that one stays
// a tiny Set. Self-polls every 10s while any box is still setting up so
// the "Setting up" cards animate without a manual Refresh, then stops.
// project_managed_cloud_onboarding_gap.
// Returns a `refresh` alongside the list: after the user presses Wake or Pause
// the row changes server-side within a second, but the 10s poll meant the card
// sat unchanged long enough to read as an ignored click.
function useManagedMachines(
  token: string | null | undefined,
): { machines: ManagedMachineSummary[]; refresh: () => void } {
  const [machines, setMachines] = useState<ManagedMachineSummary[]>([]);
  const [nonce, setNonce] = useState(0);
  const refresh = useCallback(() => setNonce((n) => n + 1), []);
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/subscription`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (res.ok) {
          const data = await res.json().catch(() => ({}));
          const list: ManagedMachineSummary[] = (
            Array.isArray(data?.machines) ? data.machines : []
          ).map((m: Record<string, unknown>) => ({
            id: String(m?.id ?? ""),
            machineType: typeof m?.machineType === "string" ? m.machineType : "cpu",
            status: typeof m?.status === "string" ? m.status : "",
            hostname: typeof m?.hostname === "string" ? m.hostname : null,
            serverIp: typeof m?.serverIp === "string" ? m.serverIp : null,
            region: typeof m?.region === "string" ? m.region : null,
            deviceId: typeof m?.deviceId === "string" ? m.deviceId : null,
            provisionPhase:
              typeof m?.provisionPhase === "string" ? m.provisionPhase : null,
            provisionProgress:
              typeof m?.provisionProgress === "number" ? m.provisionProgress : null,
            provisionError:
              typeof m?.provisionError === "string" ? m.provisionError : null,
            // errorMessage was DECLARED on the summary and RENDERED by the
            // awaiting-auth branch, but never mapped — the source array is
            // `any`, so TS could not catch the gap. The control plane's exact
            // recovery sentence ("its Yaver agent session expired…") has been
            // sent on /subscription the whole time and silently dropped here,
            // leaving the generic fallback as the only thing users ever saw.
            errorMessage:
              typeof m?.errorMessage === "string" ? m.errorMessage : null,
            provisionPhaseAt:
              typeof m?.provisionPhaseAt === "number" ? m.provisionPhaseAt : null,
            lastWokeAt: typeof m?.lastWokeAt === "number" ? m.lastWokeAt : null,
            providerStatus:
              typeof m?.providerStatus === "string" ? m.providerStatus : null,
            providerStatusAt:
              typeof m?.providerStatusAt === "number" ? m.providerStatusAt : null,
            lastWakeDurationMs:
              typeof m?.lastWakeDurationMs === "number" ? m.lastWakeDurationMs : null,
            lastWakeOutcome:
              typeof m?.lastWakeOutcome === "string" ? m.lastWakeOutcome : null,
            lastParkedAt: typeof m?.lastParkedAt === "number" ? m.lastParkedAt : null,
            snapshotSizeGb:
              typeof m?.snapshotSizeGb === "number" ? m.snapshotSizeGb : null,
            snapshotCreatedAt:
              typeof m?.snapshotCreatedAt === "number" ? m.snapshotCreatedAt : null,
            hasVolume: Boolean(m?.hasVolume),
            runnersAuthorized: Boolean(m?.runnersAuthorized),
          }));
          if (cancelled) return;
          setMachines(list);
          const anyPending = list.some(
            (m) =>
              m.status !== "removed" &&
              m.status !== "stopped" &&
              m.status !== "stopping" &&
              m.provisionPhase !== "ready" &&
              m.status !== "active",
          );
          if (anyPending) timer = setTimeout(tick, 10_000);
          return;
        }
        // A non-ok response used to fall straight through to the end of the
        // function WITHOUT rescheduling, so a single 502 or expired-token
        // blip killed the poll for the lifetime of the mount: the wake UI
        // froze on whatever it had last seen and never recovered, which is
        // itself one of the ways "Resuming…" appeared to hang. Same for the
        // catch below. Retry instead — a slower poll is not a dead one.
        if (!cancelled) timer = setTimeout(tick, 15_000);
      } catch {
        if (!cancelled) timer = setTimeout(tick, 15_000);
      }
    };
    void tick();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
    // `nonce` re-runs the effect, which cancels the pending timer and fires a
    // fresh tick immediately — that IS the refresh.
  }, [token, nonce]);
  return { machines, refresh };
}

/**
 * ManagedStateChip — says what a Yaver-hosted box actually IS right now.
 *
 * Every non-running managed box used to render as plain "Offline", so a box
 * parked to save money (wakeable in ~2-3 min) was indistinguishable from one
 * that had been deleted and can never come back. That is the difference between
 * "click Wake" and "provision a new one", and the dashboard said neither.
 */
function ManagedStateChip({ device }: { device: Device }) {
  if (!isManagedCloudDevice(device)) return null;
  const state = describeMachineState(device.machineStatus, device.machineWakeable === true);
  if (state.tone === "running") return null; // the normal online dot already says this

  const tone =
    state.tone === "asleep"
      ? "border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-300"
      : state.tone === "busy"
        ? "border-violet-300 bg-violet-50 text-violet-700 dark:border-violet-500/40 dark:bg-violet-500/10 dark:text-violet-300"
        : "border-surface-300 bg-surface-100 text-surface-600 dark:border-surface-600 dark:bg-surface-800 dark:text-surface-400";

  return (
    <span
      title={state.hint}
      className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${tone}`}
    >
      {state.tone === "asleep" ? "🌙" : state.tone === "busy" ? "⏳" : "⊘"} {state.label}
    </span>
  );
}

/**
 * ManagedPowerButton — Pause / Resume a Yaver-hosted box from the WEB dashboard.
 * Mobile has had this (Devices tab ⏸ Pause / ▶ Resume); web did not, so the only
 * way to stop the meter from a laptop was the phone. Pause = snapshot then DELETE
 * the server (Hetzner bills stopped servers, so scale-to-zero must delete);
 * Resume recreates it from the snapshot.
 */
function ManagedPowerButton({
  device,
  token,
  onDone,
}: {
  device: Device;
  token: string | null | undefined;
  /** Called with what was requested, the moment the control plane accepts it. */
  onDone?: (kind: "wake" | "park") => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  if (!token || !isManagedCloudDevice(device) || !device.machineId) return null;

  // "Asleep" and "gone" are different, and conflating them produced the two bugs
  // this block now prevents:
  //
  //  - A `removed`/`error` box is NOT paused, so the old isMachinePaused() check
  //    fell through to the else-branch and offered ⏸ PAUSE on a box that had
  //    already been deleted. Pausing a box that does not exist is nonsense.
  //  - Wakeability also needs a snapshot to recreate FROM, which only the
  //    backend knows. The web used to guess from status alone and offered
  //    ▶ Resume on snapshot-less boxes that wakeMachine then refused.
  //
  // machineWakeable is the backend's own verdict (isMachineWakeable), so the
  // button now offers exactly the action that will actually succeed.
  const wakeable = device.machineWakeable === true;
  const running = isMachineRunning(device.machineStatus);
  if (!wakeable && !running) {
    // Gone, or mid-transition: no power action is meaningful. The state chip
    // (ManagedStateChip) says what happened; a button here would only mislead.
    return null;
  }
  const paused = wakeable;

  const act = async () => {
    if (!device.machineId || !token) return;
    if (
      !paused &&
      !window.confirm(
        `Pause ${device.name || "this box"}?\n\nYaver snapshots it, then DELETES the server so billing stops. Resume recreates it from the snapshot (~2-3 min).`,
      )
    ) {
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      if (paused) await startManagedCloudMachine(token, device.machineId);
      else await stopManagedCloudMachine(token, device.machineId);
      // Only after the POST resolves: an optimistic bar started on click would
      // keep creeping even when the request was refused.
      onDone?.(paused ? "wake" : "park");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <button
      onClick={() => void act()}
      disabled={busy}
      className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider transition-colors disabled:opacity-60 ${
        paused
          ? "border-emerald-300 bg-emerald-50 text-emerald-700 hover:bg-emerald-500/20 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-300"
          : "border-amber-300 bg-amber-50 text-amber-700 hover:bg-amber-500/20 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
      }`}
      title={
        paused
          ? "Wake this box — recreate it from its latest snapshot (~2-3 min)"
          : "Pause this box — snapshot then delete the server so billing stops (scale to zero)"
      }
    >
      {busy ? "…" : paused ? "▶ Wake" : "⏸ Pause"}
      {err ? (
        <span className="ml-1 normal-case text-red-600 dark:text-red-300" title={err}>
          ✗
        </span>
      ) : null}
    </button>
  );
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
  onNavigateCloud,
}: DevicesViewProps) {
  const agentConnectionState = useAgentConnectionState();
  const failureRegistryVersion = useFailureRegistryVersion();
  const { primaryDeviceId, setPrimaryDevice, secondaryDeviceId, setSecondaryDevice } = usePrimaryDeviceId(token);
  const managedDeviceIds = useManagedDeviceIds(token);
  const { machines: managedMachines, refresh: refreshManagedMachines } =
    useManagedMachines(token);
  // Wake/park the user just asked for, keyed by machineId, so the card can show
  // the request the instant it's made rather than after the next poll.
  const [pendingPower, setPendingPower] = useState<
    Record<string, { kind: "wake" | "park"; at: number }>
  >({});
  const deviceIdSet = useMemo(() => new Set(devices.map((d) => d.id)), [devices]);
  // deviceId → managed-machine summary, so a device card can show its
  // cloud lifecycle state (paused/resuming) and a Pause/Resume action.
  const managedByDeviceId = useMemo(() => {
    const map = new Map<string, ManagedMachineSummary>();
    for (const m of managedMachines) {
      if (m.deviceId) map.set(m.deviceId, m);
    }
    return map;
  }, [managedMachines]);
  // Which managed box has a pause/resume call in flight (its machineId).
  const [boxBusy, setBoxBusy] = useState<string | null>(null);
  // Pause (snapshot + delete the server to stop billing) / Resume
  // (recreate from the snapshot) a managed box — the same Convex
  // billing routes mobile and the Managed Cloud panel use.
  async function pauseResumeBox(machineId: string, action: "stop" | "start") {
    if (!token) return;
    setBoxBusy(machineId);
    try {
      const res = await fetch(`${CONVEX_URL}/billing/yaver-cloud/${action}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ machineId }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok) {
        alert(j?.error || `${action === "stop" ? "Pause" : "Resume"} failed (${res.status})`);
      }
    } catch (e: any) {
      alert(e?.message || String(e));
    } finally {
      setBoxBusy(null);
      void onRefresh();
    }
  }
  // Managed boxes that exist in cloudMachines but have not yet produced
  // a real `devices` heartbeat row → render a synthetic "Setting up"
  // card so the box is first-class the moment it's bought, not a void
  // until it boots. Once it heartbeats, deviceIdSet contains its
  // deviceId and the normal full card (Shell/SSH/Coding Agents) takes
  // over — the synthetic card disappears. removed/stopped boxes are
  // intentionally hidden (commit 4e2112bb).
  const pendingManagedBoxes = useMemo(
    () =>
      managedMachines.filter(
        (m) =>
          m.status !== "removed" &&
          m.status !== "stopped" &&
          m.status !== "stopping" &&
          m.status !== "paused" &&
          m.status !== "suspended" &&
          !(m.deviceId && deviceIdSet.has(m.deviceId)),
      ),
    [managedMachines, deviceIdSet],
  );
  // A PARKED box with no live device card of its own. Scale-to-zero deletes the
  // server, so once the box's device row goes stale (or it never registered one)
  // the box exists only as a cloudMachines row + a snapshot — and it rendered
  // nowhere at all, which is exactly the moment the user needs a Wake button.
  // pendingManagedBoxes deliberately excludes paused/suspended because its card
  // is a provisioning-progress card; a parked box is not provisioning, so it gets
  // its own card here rather than a misleading "Setup failed" bar.
  //
  // Wakeability is NOT re-derived here: /subscription only returns a paused or
  // suspended box that still has a recovery pointer to wake from, so every box
  // reaching this list is genuinely wakeable (see isMachineWakeable — the client
  // must never re-implement that rule).
  const parkedManagedBoxes = useMemo(
    () =>
      managedMachines.filter(
        (m) =>
          (m.status === "paused" || m.status === "suspended") &&
          !(m.deviceId && deviceIdSet.has(m.deviceId)),
      ),
    [managedMachines, deviceIdSet],
  );
  const { primaryRunnerByDevice, primaryModelByDevice, primaryProviderByDevice, setPrimaryRunner } = usePrimaryRunnerByDevice(token);
  // Phase C: which device (if any) has the recycle dialog open. The
  // dialog is a fixed overlay so it can render inline next to the
  // trigger button; the agent owns every safety guard.
  const [recycleFor, setRecycleFor] = useState<{ id: string; name: string } | null>(null);
  // Backfill provider/model for opencode devices whose Convex row is
  // half-populated (runnerId only). Reads opencode.json over the relay
  // so the dropdowns show the device's actual model (e.g. zai/glm-4.7)
  // instead of the static catalogue's first entry.
  const liveOpenCodeByDevice = useLiveOpenCodeByDevice(
    devices,
    primaryRunnerByDevice,
    primaryProviderByDevice,
    primaryModelByDevice,
    agentConnectionState === "connected",
  );
  // Latest released agent version from GitHub. Drives the per-device
  // "✓ latest" / "update available" badge + the remote-update button.
  const latestAgentVersion = useLatestAgentVersion();
  const [updateModalDevice, setUpdateModalDevice] = useState<Device | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [authModal, setAuthModal] = useState<{ device: Device; runner: string } | null>(null);
  const [codingAgentModalDeviceId, setCodingAgentModalDeviceId] = useState<string | null>(null);
  // The "Rescue" inline panel — Convex-backed command queue that
  // works even when a device's relay tunnel is wedged (the agent's
  // heartbeat polls Convex on a separate path). Tracks which device's
  // panel is open + the latest queued command for status feedback.
  const [rescueOpenDeviceId, setRescueOpenDeviceId] = useState<string | null>(null);
  // Browser-shell modal state. Lives at the DevicesView level so the
  // Shell item in each card's "⋯" menu opens the same modal as the
  // home tab, including the reauth-required guidance when the agent's
  // session has expired.
  const [shellDevice, setShellDevice] = useState<Device | null>(null);
  const [rescueStatus, setRescueStatus] = useState<Record<string, { msg: string; tone: "info" | "ok" | "err" } | undefined>>({});
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
              className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs font-medium text-amber-700 dark:text-amber-200 hover:bg-amber-500/15"
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

      {pendingManagedBoxes.length > 0 ? (
        <div className="mb-4 space-y-2">
          {pendingManagedBoxes.map((m) => {
            const failed = m.status === "error" || m.provisionPhase === "error";
            const awaitingAuth = isAwaitingYaverAuth(m);
            const pct =
              typeof m.provisionProgress === "number"
                ? m.provisionProgress
                : m.status === "provisioning"
                  ? 10
                  : 5;
            const label = m.provisionPhase
              ? PROVISION_PHASE_LABEL[m.provisionPhase] ?? m.provisionPhase
              : "initializing…";
            const name =
              m.hostname || m.deviceId || `cloud-${m.id.slice(0, 8)}`;
            return (
              <div
                key={m.id}
                className={`card p-4 ${failed ? "border border-red-500/30" : "border border-sky-500/20"}`}
              >
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-sky-500/15 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-sky-700 dark:text-sky-300">
                      Yaver Managed Cloud
                    </span>
                    <span className="text-sm font-medium text-surface-100">
                      {name}
                    </span>
                    <span className="text-xs text-surface-500">
                      {m.machineType.toUpperCase()}
                      {m.region ? ` · ${m.region}` : ""}
                    </span>
                  </div>
                  <span
                    className={`text-xs font-medium ${
                      failed
                        ? "text-red-700 dark:text-red-300"
                        : awaitingAuth
                          ? "text-amber-700 dark:text-amber-300"
                          : "text-sky-700 dark:text-sky-300"
                    }`}
                  >
                    {failed
                      ? "Setup failed"
                      : awaitingAuth
                        ? "Sign-in needed"
                        : "Setting up"}
                  </span>
                </div>
                {failed ? (
                  <div className="mt-2">
                    <p className="text-xs text-red-700 dark:text-red-300">
                      {m.provisionError
                        ? m.provisionError
                        : "Provisioning failed before the agent came online."}
                    </p>
                    <p className="mt-1 text-[11px] text-surface-500">
                      Recovery: remove this box from Billing and buy a fresh
                      one. If it keeps failing, the operator can SSH in (the
                      MANAGED_CLOUD_SSH_PUBKEY debug key) and read{" "}
                      <code className="rounded bg-surface-800 px-1 py-0.5">
                        docker logs yaver
                      </code>
                      .
                    </p>
                  </div>
                ) : awaitingAuth ? (
                  // No progress bar: nothing is happening and nothing will,
                  // until this box is signed in. The control plane already
                  // writes the exact sentence — show it rather than inventing
                  // a vaguer one.
                  <div className="mt-2">
                    <p className="text-xs text-amber-700 dark:text-amber-300">
                      {m.errorMessage ??
                        "This box is awake, but its Yaver session expired so it can't finish connecting on its own."}
                    </p>
                    <p className="mt-1.5 text-[11px] text-surface-500">
                      Sign it in from the Yaver app on your phone — open the
                      remote-box picker and use “Sign this machine in”. It will
                      not connect by itself, and it parks again once the wake
                      window closes.
                    </p>
                  </div>
                ) : (
                  <div className="mt-2">
                    <div className="mb-1 text-[11px] text-surface-400">
                      {label}
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded bg-surface-800">
                      <div
                        className="h-full rounded bg-sky-500 transition-all duration-700"
                        style={{ width: `${Math.max(5, Math.min(100, pct))}%` }}
                      />
                    </div>
                    <p className="mt-1.5 text-[11px] text-surface-500">
                      This becomes a full device card (Shell, SSH, Coding
                      Agents) automatically once the box finishes booting and
                      connects.
                    </p>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      ) : null}

      {parkedManagedBoxes.length > 0 ? (
        <div className="mb-4 space-y-2">
          {parkedManagedBoxes.map((m) => {
            const state = describeMachineState(m.status, true);
            const name = m.hostname || m.deviceId || `cloud-${m.id.slice(0, 8)}`;
            const busy = boxBusy === m.id;
            return (
              <div key={m.id} className="card border border-amber-500/20 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-sky-500/15 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-sky-700 dark:text-sky-300">
                      Yaver Managed Cloud
                    </span>
                    <span className="text-sm font-medium text-surface-100">{name}</span>
                    <span className="text-xs text-surface-500">
                      {m.machineType.toUpperCase()}
                      {m.region ? ` · ${m.region}` : ""}
                    </span>
                  </div>
                  <span className="text-xs font-medium text-amber-700 dark:text-amber-300">
                    {state.label}
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
                  <p className="text-[11px] text-surface-500">{state.hint}</p>
                  <button
                    onClick={() => void pauseResumeBox(m.id, "start")}
                    disabled={busy}
                    className="rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-500/20 disabled:opacity-60 dark:text-amber-300"
                  >
                    {busy ? "Waking…" : "▶ Wake"}
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      ) : null}

      {renderedDevices.length === 0 &&
      pendingManagedBoxes.length === 0 &&
      parkedManagedBoxes.length === 0 ? (
        <div className="card p-8 text-center">
          <p className="mb-2 text-sm text-surface-400">No devices registered.</p>
          {dormantDevices.length > 0 ? (
            <p className="mb-3 text-xs text-amber-700 dark:text-amber-300">
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
                className="text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300"
              >
                Show all
              </button>
            </div>
          ) : null}
          {!showDormantDevices && dormantDevices.length > 0 ? (
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-xs text-amber-700 dark:text-amber-200">
              {dormantDevices.length} stale device{dormantDevices.length === 1 ? "" : "s"} hidden because they have no recent agent signal and no usable relay/tunnel path.
            </div>
          ) : null}
          {/* HN-LAUNCH-HIDE-PAID: hide the "Yaver Cloud — rent a managed box" banner. */}
          {onNavigateCloud && !HIDE_PAID_UI ? (
            <ManagedCloudSummary token={token} onOpen={onNavigateCloud} />
          ) : null}
          {renderedDevices.map((device) => {
            const shareSummary = deviceShareSummary(device);
            const isActiveWorkspace = activeWorkspaceDeviceId === device.id;
            const sshCommand = sshCommandForDevice(device);
            const directSSHHost = directSSHHostForDevice(device);
            const sshHref = directSSHHost ? `ssh://${directSSHHost}` : null;
            const managedMachine = managedByDeviceId.get(device.id);
            // Heartbeat-alive and browser-reachable are different questions —
            // see lib/device-lifecycle.ts. Every CTA below that performs
            // browser→agent I/O gates on `canAct`, not on device.online alone.
            // `failureRegistryVersion` is read at component scope so this
            // re-derives when a background probe records a failure (we can't
            // call the subscribe hook per-iteration inside this map).
            void failureRegistryVersion;
            const lifecycle = deriveDeviceLifecycleState(device);
            const reach = deriveBrowserReach(device, getLastFailure(device.id));
            const canAct = canBrowserActOnDevice(lifecycle, reach);
            return (
            <div key={device.id} className="card flex items-start gap-4 border border-slate-200 bg-white shadow-sm dark:border-surface-700/80 dark:bg-[rgba(44,46,56,0.82)] dark:shadow-[0_18px_40px_rgba(0,0,0,0.22),inset_0_1px_0_rgba(255,255,255,0.03)]">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-100 text-slate-500 dark:bg-[rgba(18,19,24,0.92)] dark:text-surface-300">
                <DeviceIcon
                  platform={device.platform}
                  managed={managedDeviceIds.has(device.id)}
                  label={devicePlatformLabel(device)}
                />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="font-semibold text-slate-900 dark:text-surface-50">
                        {device.name}
                      </h3>
                      {device.alias ? (
                        <span
                          className="rounded-full border border-emerald-300 bg-emerald-50 px-2 py-0.5 font-mono text-[10px] font-semibold text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-200"
                          title={`Alias used by \`yaver ssh @${device.alias}\` and the dashboard. Edit from the home tab card or run \`yaver alias set ${device.id.slice(0, 8)} <new>\`.`}
                        >
                          @{device.alias}
                        </span>
                      ) : null}
                      {device.isGuest ? (
                        <span className="rounded border border-sky-300 bg-sky-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-700 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200">
                          Shared Device
                        </span>
                      ) : null}
                      {device.deviceClass ? (
                        <span className="rounded border border-sky-300 bg-sky-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-700 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-200">
                          {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
                        </span>
                      ) : null}
                      {!device.isGuest && device.sessionBinding ? (
                        <span
                          className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                            device.sessionBinding === "dedicated"
                              ? "border-info/30 bg-info-soft text-info-softFg dark:border-info/30 dark:bg-info-soft dark:text-info-softFg"
                              : "border-warning/30 bg-warning-soft text-warning-softFg dark:border-warning/30 dark:bg-warning-soft dark:text-warning-softFg"
                          }`}
                        >
                          {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Shared Session"}
                        </span>
                      ) : null}
                      {!device.isGuest ? (
                        <span
                          className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                            managedDeviceIds.has(device.id)
                              ? "border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200"
                              : "border-slate-300 bg-slate-50 text-slate-600 dark:border-surface-700 dark:bg-surface-800/40 dark:text-surface-300"
                          }`}
                          title={
                            managedDeviceIds.has(device.id)
                              ? "Provisioned or adopted by Yaver managed cloud"
                              : "Your own hardware or cloud box (self-hosted)"
                          }
                        >
                          {managedDeviceIds.has(device.id) ? "Yaver Managed Cloud" : "Self-hosted"}
                        </span>
                      ) : null}
                      {managedMachine &&
                      (managedMachine.status === "paused" ||
                        managedMachine.status === "suspended") ? (
                        <span
                          className="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
                          title="Box is paused — snapshot kept, ~€0.50/mo. Resume to bring it back."
                        >
                          ⏸ Paused
                        </span>
                      ) : null}
                      {/* A box blocked on sign-in is still status="resuming"
                          server-side (the control plane holds it there for a
                          bounded recovery window), so a chip keyed only on
                          status printed "Resuming…" for a wake that had
                          already stopped making progress and never would
                          again. Key the chip on the phase, not the status. */}
                      {managedMachine && managedMachine.status === "resuming" ? (
                        isAwaitingYaverAuth(managedMachine) ? (
                          <span
                            className="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
                            title="The box is awake but its Yaver session expired — it needs signing in."
                          >
                            Sign-in needed
                          </span>
                        ) : (
                          <span className="rounded border border-sky-300 bg-sky-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-700 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-300">
                            Waking…
                          </span>
                        )
                      ) : null}
                      {/* Primary / Secondary are now set from the "⋯"
                          menu, so the card has to carry the state itself. */}
                      {primaryDeviceId === device.id ? (
                        <span
                          className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand-soft px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-brand-softFg"
                          title="This is your primary device"
                        >
                          <svg className="h-3 w-3" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                            <path d="m12 2.75 2.33 4.72 5.21.76-3.77 3.67.89 5.19L12 14.6l-4.66 2.49.89-5.19-3.77-3.67 5.21-.76L12 2.75Z" />
                          </svg>
                          Primary
                        </span>
                      ) : secondaryDeviceId === device.id ? (
                        <span
                          className="inline-flex items-center gap-1 rounded border border-violet-400/50 bg-violet-100 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-violet-700 dark:border-violet-400/40 dark:bg-violet-500/10 dark:text-violet-300"
                          title="This is your fallback secondary device"
                        >
                          <svg className="h-3 w-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} aria-hidden>
                            <path d="m12 2.75 2.33 4.72 5.21.76-3.77 3.67.89 5.19L12 14.6l-4.66 2.49.89-5.19-3.77-3.67 5.21-.76L12 2.75Z" />
                          </svg>
                          Secondary
                        </span>
                      ) : null}
                      <DeviceLifecycleBadge device={device} />
                    </div>
                    <div className="mt-1 flex flex-wrap items-center gap-1">
                      <TransportBadge device={device} />
                      {/* Only BYO gets its own chip. "yaver-hosted" and
                          "self-hosted" already have a badge above, and
                          rendering both printed SELF-HOSTED twice on the
                          same card. */}
                      {device.hosting === "byo" ? (
                        <span
                          className="inline-flex items-center gap-1 rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
                          title="Yaver-provisioned on your own cloud account — auto scale-to-zero to cut your provider bill"
                        >
                          BYO
                        </span>
                      ) : null}
                      {/* What the box IS (Asleep / Waking / Gone) before what you
                          can DO to it — "Offline" alone can't tell a parked box
                          from a deleted one. */}
                      <ManagedStateChip device={device} />
                      {/* Yaver-managed box → Pause (snapshot+delete, meter stops)
                          / Wake, same control the mobile Devices tab has. Renders
                          nothing when neither action would succeed. */}
                      <ManagedPowerButton
                        device={device}
                        token={token}
                        onDone={(kind) => {
                          if (device.machineId) {
                            setPendingPower((p) => ({
                              ...p,
                              [device.machineId as string]: { kind, at: Date.now() },
                            }));
                          }
                          refreshManagedMachines();
                        }}
                      />
                    </div>
                    {/* The wake itself: which step, how long it has been on
                        it, what the provider sees, and — when the box is only
                        blocked on sign-in — what to actually do. A managed box
                        takes minutes to wake, and until now the card said
                        nothing for the whole of it. */}
                    {managedMachine &&
                    (managedMachine.status === "resuming" ||
                      managedMachine.status === "stopping" ||
                      managedMachine.status === "grace" ||
                      // Requested but not yet reflected server-side — the bar
                      // has to appear on click, not on the next poll.
                      !!pendingPower[managedMachine.id]) ? (
                      <WakeProgress
                        machine={managedMachine}
                        optimistic={pendingPower[managedMachine.id] ?? null}
                        deviceReachable={(() => {
                          const lc = deriveDeviceLifecycleState(device);
                          return lc === "connected" || lc === "ready-to-connect";
                        })()}
                      />
                    ) : managedMachine &&
                      (managedMachine.status === "paused" ||
                        managedMachine.status === "suspended") ? (
                      // At rest: say what's kept, how long a wake takes on THIS
                      // box, and — the part that was missing entirely — why the
                      // last wake didn't stick, if it didn't.
                      <ParkedSummary machine={managedMachine} />
                    ) : null}
                    {/* Signal · version · update all on ONE line — the update
                        affordance used to sit on its own row under the identity
                        line and pushed the card taller for a chip most devices
                        never show. The platform is NOT written here: the card's
                        icon is now the platform's own mark, and printing
                        "Linux" beside a penguin is the same fact twice. The
                        full label (including "likely WSL") lives on the icon. */}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-slate-600 dark:text-surface-400">
                      <span>
                        Last agent signal {formatLastSeen(device.lastSeen)}
                        {device.agentVersion ? (
                          <>
                            {" "}· v{String(device.agentVersion).replace(/^v/i, "")}
                            {latestAgentVersion ? (() => {
                              const cur = String(device.agentVersion).replace(/^v/i, "");
                              const cmp = compareSemver(cur, latestAgentVersion);
                              if (cmp >= 0) {
                                return (
                                  <span title={`Latest agent (v${latestAgentVersion})`} className="ml-1 text-emerald-600 dark:text-emerald-400">✓</span>
                                );
                              }
                              return null;
                            })() : null}
                          </>
                        ) : null}
                      </span>
                      {device.agentVersion && latestAgentVersion && compareSemver(String(device.agentVersion).replace(/^v/i, ""), latestAgentVersion) < 0 ? (() => {
                            const lc = lifecycle;
                            // Gate on browser reachability too, not just on the
                            // heartbeat-derived lifecycle. A box that heartbeats
                            // but has no relay path used to get the confident
                            // amber button; clicking it always ended in "Update
                            // queued · Couldn't connect: Could not reach agent".
                            const reachable = canAct;
                            const cur = String(device.agentVersion).replace(/^v/i, "");
                            if (reachable) {
                              return (
                                <button
                                  onClick={() => setUpdateModalDevice(device)}
                                  title={`Update v${cur} → v${latestAgentVersion} on ${device.name}`}
                                  className="rounded-full border border-amber-300 bg-amber-50 px-2 py-px text-[10px] font-semibold uppercase tracking-wider text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300 dark:hover:bg-amber-500/20"
                                >
                                  update → v{latestAgentVersion}
                                </button>
                              );
                            }
                            // Lifecycle is bootstrap / yaver-auth-expired / offline:
                            // the agent is unreachable from the browser, so POST
                            // /agent/update would fail at the network layer.
                            // Show a muted chip that explains why instead of an
                            // amber button that throws "AgentClient is not
                            // connected" on click.
                            const hint = reach.unreachable
                              ? `v${cur} → v${latestAgentVersion} available — the agent is alive but this browser can't reach it (${reach.label}). Queue it and it installs on the device's next check-in.`
                              : lc === "yaver-auth-expired" || lc === "bootstrap"
                                ? `v${cur} → v${latestAgentVersion} available — re-auth from CLI first (yaver primary auth, or yaver auth on the box)`
                                : `v${cur} → v${latestAgentVersion} available — device is offline, bring it back online first`;
                            // Still clickable when the box is merely unreachable:
                            // the update modal falls back to a Convex-queued
                            // desired-version that installs on next check-in.
                            // Muted styling so it doesn't promise an instant apply.
                            return reach.unreachable ? (
                              <button
                                onClick={() => setUpdateModalDevice(device)}
                                title={hint}
                                className="rounded-full border border-slate-300 bg-slate-50 px-2 py-px text-[10px] font-semibold uppercase tracking-wider text-slate-500 hover:bg-slate-100 dark:border-surface-700 dark:bg-surface-800/40 dark:text-surface-400 dark:hover:bg-surface-800"
                              >
                                queue update → v{latestAgentVersion}
                              </button>
                            ) : (
                              <span
                                title={hint}
                                className="rounded-full border border-slate-300 bg-slate-50 px-2 py-px text-[10px] font-semibold uppercase tracking-wider text-slate-500 dark:border-surface-700 dark:bg-surface-800/40 dark:text-surface-400"
                              >
                                update → v{latestAgentVersion} (unreachable)
                              </span>
                            );
                      })() : null}
                      {device.probeState === "ok" && device.probePath ? (
                        <span>· probed via {device.probePath}</span>
                      ) : null}
                      {device.probeState === "auth-expired" ? <span>· auth expired</span> : null}
                    </div>
                  </div>

                  <div className="flex shrink-0 flex-wrap items-center gap-2 xl:justify-end">
                    {!device.isGuest && token && managedMachine && managedMachine.status === "active" ? (
                      <button
                        disabled={boxBusy === managedMachine.id}
                        onClick={() => {
                          if (
                            !window.confirm(
                              "Pause this box? It snapshots the disk, then deletes the cloud " +
                                "server so it stops billing — ~€0.50/mo while paused vs ~€30/mo " +
                                "running. Resume recreates it from the snapshot in ~2-3 min (new IP).",
                            )
                          )
                            return;
                          void pauseResumeBox(managedMachine!.id, "stop");
                        }}
                        className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-amber-300 bg-amber-50 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-amber-700 transition-colors hover:border-amber-400 disabled:opacity-50 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
                        title="Pause: snapshot + delete the server to stop billing — resumable"
                      >
                        {boxBusy === managedMachine.id ? "…" : "⏸ Pause box"}
                      </button>
                    ) : null}
                    {!device.isGuest && token && managedMachine && (managedMachine.status === "paused" || managedMachine.status === "suspended") ? (
                      <button
                        disabled={boxBusy === managedMachine.id}
                        onClick={() => void pauseResumeBox(managedMachine!.id, "start")}
                        className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-emerald-300 bg-emerald-50 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-emerald-700 transition-colors hover:border-emerald-400 disabled:opacity-50 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-300"
                        title="Resume: recreate the box from its pause snapshot (~2-3 min)"
                      >
                        {boxBusy === managedMachine.id ? "…" : "▶ Resume box"}
                      </button>
                    ) : null}
                    {recycleFor?.id === device.id && token ? (
                      <RecycleBoxDialog
                        device={device}
                        devices={devices}
                        primaryDeviceId={primaryDeviceId}
                        token={token}
                        onClose={() => {
                          setRecycleFor(null);
                          // A successful remove deregisters the box's
                          // Convex row; pull fresh so the card drops
                          // immediately instead of lingering as a
                          // ghost until the next poll.
                          void onRefresh();
                        }}
                      />
                    ) : null}
                    <DeviceActionsMenu
                      device={device}
                      token={token}
                      sshHref={sshHref}
                      sshCommand={sshCommand}
                      isPrimary={primaryDeviceId === device.id}
                      isSecondary={secondaryDeviceId === device.id}
                      detailsOpen={expandedId === device.id}
                      rescueOpen={rescueOpenDeviceId === device.id}
                      onSetPrimary={async () => {
                        try {
                          await setPrimaryDevice(primaryDeviceId === device.id ? null : device.id);
                        } catch (e: any) {
                          alert(`Failed to update primary: ${e?.message ?? e}`);
                        }
                      }}
                      onSetSecondary={async () => {
                        try {
                          await setSecondaryDevice(secondaryDeviceId === device.id ? null : device.id);
                        } catch (e: any) {
                          alert(`Failed to update secondary: ${e?.message ?? e}`);
                        }
                      }}
                      onRecycle={() => setRecycleFor({ id: device.id, name: device.alias || device.name || device.id })}
                      onRescue={() => setRescueOpenDeviceId(rescueOpenDeviceId === device.id ? null : device.id)}
                      onShell={() => setShellDevice(device)}
                      onCodingAgent={() => setCodingAgentModalDeviceId(device.id)}
                      onToggleDetails={() => setExpandedId(expandedId === device.id ? null : device.id)}
                      onLeftShare={() => { void onRefresh(); }}
                    />
                  </div>
                </div>
                {rescueOpenDeviceId === device.id ? (
                  <RescueInlinePanel
                    device={device}
                    statusMsg={rescueStatus[device.id]}
                    onQueue={async (command) => {
                      setRescueStatus((prev) => ({
                        ...prev,
                        [device.id]: { msg: `Queueing ${command}…`, tone: "info" },
                      }));
                      try {
                        const res = await agentClient.queueRescueCommand(device.id, command);
                        const tail = res.deduped ? "(already pending)" : `(id ${res.commandId.slice(0, 8)}…)`;
                        setRescueStatus((prev) => ({
                          ...prev,
                          [device.id]: {
                            msg: `Queued ${command} ${tail} — agent picks up next heartbeat`,
                            tone: "ok",
                          },
                        }));
                      } catch (e: any) {
                        setRescueStatus((prev) => ({
                          ...prev,
                          [device.id]: { msg: e?.message || "queue failed", tone: "err" },
                        }));
                      }
                    }}
                    onReauth={async () => {
                      // Reset Auth = headless re-auth via /auth/recover
                      // (mode=direct), not the destructive
                      // "move config aside + exit" rescue path. Sends
                      // the user's already-signed-in web bearer to
                      // the agent through the relay; agent verifies
                      // ownership against Convex and rotates its
                      // token in place. Falls back to mode=pair on
                      // older agents. See agent-client.ts::reauthAgent.
                      if (!token) {
                        setRescueStatus((prev) => ({
                          ...prev,
                          [device.id]: { msg: "not signed in — refresh and try again", tone: "err" },
                        }));
                        return;
                      }
                      setRescueStatus((prev) => ({
                        ...prev,
                        [device.id]: { msg: "Re-authenticating remote agent…", tone: "info" },
                      }));
                      try {
                        const r = await agentClient.reauthAgent({
                          deviceId: device.id,
                          hostSessionToken: token,
                        });
                        if (r.ok) {
                          setRescueStatus((prev) => ({
                            ...prev,
                            [device.id]: {
                              msg: `Re-auth ok via ${r.via} (${r.mode}). Refreshing…`,
                              tone: "ok",
                            },
                          }));
                          setTimeout(() => onRefresh().catch(() => {}), 1200);
                        } else {
                          const summary = r.diagnostics
                            .map((d) => `${d.path}/${d.step}: ${d.ok ? "ok" : d.error || "fail"}`)
                            .join(" · ");
                          setRescueStatus((prev) => ({
                            ...prev,
                            [device.id]: {
                              msg: `Re-auth failed${r.error ? `: ${r.error}` : ""}. ${summary}`,
                              tone: "err",
                            },
                          }));
                        }
                      } catch (e: any) {
                        setRescueStatus((prev) => ({
                          ...prev,
                          [device.id]: { msg: e?.message || "re-auth crashed", tone: "err" },
                        }));
                      }
                    }}
                    onClose={() => setRescueOpenDeviceId(null)}
                  />
                ) : null}
                {device.edgeProfile ? (
                  <p className="text-xs text-slate-500 dark:text-surface-400">
                    {device.edgeProfile.supportsLocalInference ? "Local inference" : "No local inference"} · max {device.edgeProfile.maxModelClass} model · {device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}
                  </p>
                ) : null}
                {shareSummary?.viewerIsGuest && shareSummary?.hostLabel ? (
                  <div className="mt-3">
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-400">
                      Shared from
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span
                        className="inline-flex items-center gap-1.5 rounded-full border border-sky-300 bg-sky-50 py-0.5 pl-0.5 pr-2.5 text-xs text-sky-700 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-100"
                      >
                        <span className="flex h-5 w-5 items-center justify-center rounded-full bg-sky-200 text-[10px] font-semibold uppercase text-sky-800 dark:bg-sky-500/30 dark:text-sky-50">
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
                          ? "border-sky-300 bg-sky-50 text-sky-700 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200"
                          : "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200"
                      }`}>
                        {shareSummary.projectLabel}
                      </span>
                    ) : null}
                    {shareSummary.projectChips.map((project) => (
                      <span key={`${device.id}:project:${project}`} className="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200">
                        {project}
                      </span>
                    ))}
                  </div>
                ) : null}
                {shareSummary && shareSummary.guestChips.length > 0 ? (
                  <div className="mt-3">
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-400">
                      Shared with
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {shareSummary.guestChips.map((guest) => (
                        <span
                          key={`${device.id}:guest:${guest}`}
                          className="inline-flex items-center gap-1.5 rounded-full border border-sky-300 bg-sky-50 py-0.5 pl-0.5 pr-2.5 text-xs text-sky-700 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-100"
                        >
                          <span className="flex h-5 w-5 items-center justify-center rounded-full bg-sky-200 text-[10px] font-semibold uppercase text-sky-800 dark:bg-sky-500/30 dark:text-sky-50">
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
                  const explicitPrimary = primaryRunnerByDevice[device.id];
                  const seededPrimary = (() => {
                    if (explicitPrimary) return explicitPrimary;
                    const readyIds = states.filter((s) => s.health === "ready").map((s) => s.id);
                    return preferredDefaultRunnerForDevice(device, signedInEmail, readyIds);
                  })();
                  const primaryId = explicitPrimary ?? seededPrimary ?? "";
                  const primaryState = states.find((s) => s.id === primaryId);
                  return (
                    <div className="mt-3">
                      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-400">
                        Coding agents
                      </div>
                      <div className="rounded-lg border border-indigo-300 bg-indigo-50 px-3 py-2 dark:border-indigo-500/30 dark:bg-indigo-500/5">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="flex items-center gap-1 text-[10px] font-semibold uppercase tracking-widest text-indigo-700 dark:text-indigo-300">
                            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                              <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>
                            </svg>
                            Preferred
                          </span>
                          {primaryState ? (
                            <RunnerChipWithTest
                              device={device}
                              state={primaryState}
                              token={token ?? null}
                              onSignIn={(runnerId) => setAuthModal({ device, runner: runnerId })}
                            />
                          ) : (
                            <span className="text-[12px] text-slate-500 dark:text-surface-500">(none set)</span>
                          )}
                          {!explicitPrimary && seededPrimary ? (
                            <span
                              className="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/15 dark:text-amber-300"
                              title="Suggested default based on which runners are ready on this device."
                            >
                              suggested
                            </span>
                          ) : null}
                        </div>
                      </div>
                      {codingAgentModalDeviceId === device.id ? (
                        <CodingAgentModal
                          device={device}
                          token={token ?? null}
                          signedInEmail={signedInEmail}
                          primaryRunnerByDevice={primaryRunnerByDevice}
                          primaryModelByDevice={primaryModelByDevice}
                          primaryProviderByDevice={primaryProviderByDevice}
                          liveOpenCodeByDevice={liveOpenCodeByDevice}
                          setPrimaryRunner={setPrimaryRunner}
                          onSignIn={(runnerId) => setAuthModal({ device, runner: runnerId })}
                          onClose={() => setCodingAgentModalDeviceId(null)}
                        />
                      ) : null}
                      {!device.isGuest ? (
                        <DeviceProjectsRail device={device} token={token ?? null} onShowDetails={() => setExpandedId(device.id)} />
                      ) : null}
                    </div>
                  );
                })()}
                {/* One CTA. Ping / SSH / Copy SSH moved into the card's
                    "⋯" menu — they're diagnostics, not the thing you came
                    to the card to do. */}
                <div className="mt-5 flex flex-wrap items-center gap-2">
                  {isActiveWorkspace && onCloseWorkspace ? (
                    <button
                      onClick={onCloseWorkspace}
                      className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-xs font-semibold text-slate-800 shadow-sm hover:border-slate-400 hover:bg-slate-50 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.82)] dark:text-surface-100 dark:hover:border-surface-600 dark:hover:bg-[rgba(31,33,41,0.94)]"
                      title="Disconnect from this machine and close the active workspace"
                    >
                      <span aria-hidden>×</span>
                      Close Workspace
                    </button>
                  ) : onOpen ? (
                    <button
                      onClick={() => onOpen(device)}
                      className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold shadow-sm ${
                        canAct
                          ? "bg-indigo-600 text-white hover:bg-indigo-500 dark:bg-indigo-500 dark:hover:bg-indigo-400"
                          : "border border-amber-300 bg-amber-50 text-amber-700 hover:bg-amber-100 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/20"
                      }`}
                      // "Open Workspace" is a promise. Only make it when a
                      // connect can actually succeed — device.online alone is
                      // just a Convex heartbeat and says nothing about whether
                      // this browser has a path to the box.
                      title={canAct
                        ? "Connect to this machine and start working on it"
                        : reach.unreachable
                          ? `The agent is alive but this browser can't reach it (${reach.label}). Probe anyway and show relay/direct diagnostics.`
                          : "Probe this machine anyway and show relay/direct diagnostics"}
                    >
                      <span aria-hidden>⌨️</span>
                      {canAct ? "Open Workspace" : "Try Connect"}
                    </button>
                  ) : null}
                </div>
                {expandedId === device.id ? (
                  <DeviceDetailsBoundary device={device}>
                    <DeviceDetailsPanel device={device} token={token ?? null} />
                  </DeviceDetailsBoundary>
                ) : null}
              </div>
            </div>
            );
          })}
        </div>
      )}
      {shellDevice ? (
        <WebShellModal
          device={shellDevice}
          isCurrentDeviceSelected={activeWorkspaceDeviceId === shellDevice.id}
          isCurrentDeviceConnected={activeWorkspaceDeviceId === shellDevice.id && agentConnectionState === "connected"}
          onConnect={() => {
            onOpen?.(shellDevice);
          }}
          onOpenRescue={() => setRescueOpenDeviceId(shellDevice.id)}
          onClose={() => setShellDevice(null)}
        />
      ) : null}
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
      {updateModalDevice && token ? (
        <AgentUpdateModal
          device={updateModalDevice}
          latestVersion={latestAgentVersion || ""}
          token={token}
          onClose={() => {
            setUpdateModalDevice(null);
            void onRefresh();
          }}
        />
      ) : null}
    </div>
  );
}

// AgentUpdateModal triggers POST /agent/update on the connected
// device and streams the agent's progress events from
// /streams/agent-update via the same-origin proxy. While the agent
// restarts the SSE channel closes; the modal then polls /info until
// the new version reports back.
//
// Now cross-device capable: spins up a transient AgentClient and
// connects directly to the target via the existing
// relay/tunnel/LAN-fallback baseUrl ladder, same pattern
// RunnerChipWithTest.runInstall uses for cross-device runner
// installs. The dashboard's singleton agentClient stays pinned to
// whatever workspace the user has open; this modal no longer cares
// where it points.
function AgentUpdateModal({
  device,
  latestVersion,
  token,
  onClose,
}: {
  device: Device;
  latestVersion: string;
  token: string;
  onClose: () => void;
}) {
  // "connect" rather than "starting": the first thing this modal does
  // is open a transient connection to the target, which on a cold relay
  // can take seconds. Naming that phase honestly is the difference
  // between a dialog that looks stuck and one that looks busy.
  const [phase, setPhase] = useState<string>("connect");
  const [lines, setLines] = useState<Array<{ phase: string; text: string }>>([]);
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmedVersion, setConfirmedVersion] = useState<string | null>(null);
  const [downloadBytes, setDownloadBytes] = useState<{ read: number; total: number } | null>(null);
  // Set when the box was unreachable and we queued the update via Convex
  // instead. Not an error state — the update WILL happen, just not while
  // we watch. unreachableReason carries why we fell back, so the user
  // learns something about their box instead of just "queued".
  const [requested, setRequested] = useState(false);
  const [unreachableReason, setUnreachableReason] = useState<string | null>(null);
  // Tick state so the user sees something move while we wait for
  // the first SSE event from the agent. Flips every 500ms; the
  // spinner / shimmer in the modal reads from this.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (done || error || requested) return;
    const t = setInterval(() => setTick((n) => (n + 1) % 1_000_000), 500);
    return () => clearInterval(t);
  }, [done, error, requested]);

  // Transient AgentClient bound to the target device. Lives for the
  // lifetime of the modal; disconnected in cleanup. Holding it in a
  // ref so the cleanup can reach it without re-triggering the
  // useEffect on every render.
  const clientRef = useRef<AgentClient | null>(null);

  useEffect(() => {
    let cancelled = false;
    const abort = new AbortController();

    const pollForNewVersion = async () => {
      const deadline = Date.now() + 90_000; // 90s budget for the restart
      while (!cancelled && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 2500));
        try {
          // Re-resolve via /info on the live transient client. After
          // the agent restarts the QUIC/relay session may drop briefly;
          // we just retry — getInfo throws on miss, the loop swallows.
          const client = clientRef.current;
          if (!client) continue;
          const info = await client.getInfo();
          const newV = String(info?.version || "").replace(/^v/i, "");
          if (newV && (latestVersion === "" || compareSemver(newV, latestVersion) >= 0)) {
            if (!cancelled) {
              setConfirmedVersion(newV);
              setDone(true);
            }
            return;
          }
        } catch { /* network / restart in progress */ }
      }
      if (!cancelled) setError("Restart timed out — the box may need manual intervention.");
    };

    (async () => {
      try {
        // Connect a transient AgentClient directly to the target. Same
        // ladder RunnerChipWithTest.runInstall uses — relay first, then
        // tunnel URLs, then direct LAN. The dashboard's singleton
        // agentClient stays untouched.
        const client = new AgentClient();
        client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
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
        try {
          await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
        } catch (err) {
          // Unreachable is not a dead end. Fall back to desired state:
          // write the request to the device's Convex row and let the box
          // apply it on its next heartbeat. The user's intent ("update
          // this box") is satisfiable even though this browser has no
          // path to it — refusing here would strand every box that's
          // asleep, on another network, or behind a NAT we can't punch.
          //
          // The failed client never made it into clientRef, so nothing
          // else will tear it down: disconnect it here or it keeps
          // retrying its backoff ladder in the background for the life
          // of the page.
          try { client.disconnect(); } catch { /* nothing useful to do */ }
          if (cancelled) return;
          try {
            await requestAgentUpdateViaConvex(token, device.id);
            if (!cancelled) {
              setUnreachableReason(err instanceof Error ? err.message : String(err));
              setRequested(true);
            }
          } catch (reqErr) {
            if (!cancelled) {
              setError(
                `Couldn't reach ${device.name} (${err instanceof Error ? err.message : String(err)}), and queueing the update for later also failed: ${reqErr instanceof Error ? reqErr.message : String(reqErr)}`,
              );
            }
          }
          return;
        }
        if (cancelled) {
          try { client.disconnect(); } catch { /* nothing to do */ }
          return;
        }
        clientRef.current = client;
        // Reached the box. Leave the connect phase immediately — the
        // agent's first SSE event can be a second or two out, and until
        // it lands the modal would otherwise still claim to be
        // connecting when it is in fact already asking for the update.
        setPhase("queued");

        // Kick off the update on the agent. Returns started=true
        // when an update is now in flight, started=false when the
        // agent thinks it's already on the latest version. 409
        // means an update was already running — totally fine,
        // we'll just attach to the existing stream.
        let started = true;
        try {
          const triggerResp = await client.triggerAgentUpdate();
          if (triggerResp && triggerResp.started === false) {
            started = false;
            // The agent's "latest" pointer (its updateRepo) may be
            // stale — it sometimes points at a fork whose `latest`
            // tag is years behind. Surface that explicitly so the
            // user knows why no progress is happening, instead of
            // staring at "Preparing… step 1 of 8" forever.
            if (!cancelled) {
              const cv = triggerResp.currentVersion || device.agentVersion || "?";
              const lv = triggerResp.latestVersion || latestVersion || "?";
              setError(
                `The agent on ${device.name} thinks it's already up to date (it has v${cv}, says latest is v${lv}). Its auto-update repo may be stale — run \`yaver self-update --repo kivanccakmak/yaver.io\` on the box, or update via package manager (\`npm install -g yaver-cli@${lv}\`).`,
              );
              return;
            }
          }
        } catch (err) {
          // Don't fail the modal if the start call rejected with 409;
          // we still want to show the live stream of whatever update
          // is currently running.
          if (!String(err).includes("409")) throw err;
        }

        const streamUrl = client.agentUpdateStreamUrl;
        if (!streamUrl) {
          if (!cancelled) setError("Could not resolve agent stream URL — is the device connected?");
          return;
        }
        const streamRes = await fetch(streamUrl, {
          headers: client.getAuthHeaders(),
          signal: abort.signal,
        });
        if (!streamRes.ok) {
          if (!cancelled) setError(`Stream failed: HTTP ${streamRes.status}`);
          return;
        }
        // Set up a watchdog — if we get no SSE event for 45s after
        // POST returned started=true, fall back to polling /info to
        // detect a successful restart anyway. Without this, an old
        // agent that emits no progress events at all would leave the
        // modal stuck on "Preparing".
        if (started) {
          setTimeout(() => {
            if (cancelled) return;
            // Fires unconditionally rather than checking whether any
            // progress event arrived: reading phase from this closure
            // would only ever see its value at effect-setup time, so the
            // check it looks like it wants is not available here.
            // pollForNewVersion is idempotent and gives up on its own
            // deadline, so a redundant call on an agent that DID stream
            // progress costs one /info poll.
            pollForNewVersion();
          }, 45_000);
        }
        const reader = streamRes.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        let buffer = "";
        while (!cancelled) {
          const { value, done: streamDone } = await reader.read();
          if (streamDone) break;
          buffer += decoder.decode(value, { stream: true });
          const sseLines = buffer.split("\n");
          buffer = sseLines.pop() || "";
          for (const line of sseLines) {
            if (!line.startsWith("data: ")) continue;
            try {
              const ev = JSON.parse(line.slice(6));
              if (ev.type === "progress" && typeof ev.phase === "string" && typeof ev.text === "string") {
                setPhase(ev.phase);
                // Carry byte counts when present (download phase).
                if (typeof ev.bytes === "number") {
                  setDownloadBytes({ read: ev.bytes, total: typeof ev.total === "number" ? ev.total : -1 });
                }
                // Don't spam the log buffer with every percent tick;
                // collapse same-phase byte events into a single line
                // that updates in place.
                setLines((prev) => {
                  const last = prev[prev.length - 1];
                  if (last && last.phase === ev.phase && (ev.phase === "download" || ev.phase === "extract")) {
                    return [...prev.slice(0, -1), { phase: ev.phase, text: ev.text }];
                  }
                  return [...prev.slice(-30), { phase: ev.phase, text: ev.text }];
                });
                if (ev.phase === "restart") {
                  setPhase("restarting");
                  pollForNewVersion();
                }
                if (ev.phase === "error") {
                  setError(ev.text);
                }
              }
            } catch { /* ignore parse errors */ }
          }
        }
      } catch (err) {
        if (!cancelled && (err as { name?: string })?.name !== "AbortError") {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    })();

    return () => {
      cancelled = true;
      abort.abort();
      const client = clientRef.current;
      clientRef.current = null;
      if (client) {
        try { client.disconnect(); } catch { /* nothing useful to do */ }
      }
    };
  }, [device.id, latestVersion, token, device.host, device.port, device.publicEndpoints, device.tunnelUrl, device.name]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget && (done || error || requested)) onClose(); }}
    >
      <div className="w-full max-w-lg rounded-xl border border-surface-800 bg-surface-900 p-5 shadow-2xl">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <h3 className="text-base font-semibold text-surface-100">Update agent</h3>
            <p className="text-xs text-surface-500">on <span className="font-mono text-surface-300">{device.name}</span></p>
            <p className="mt-0.5 text-[10px] text-surface-600">
              v{String(device.agentVersion || "?").replace(/^v/i, "")} → v{latestVersion}
            </p>
          </div>
          <button onClick={onClose} className="text-xl leading-none text-surface-500 hover:text-surface-200">×</button>
        </div>
        {error ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-300">
            <div className="mb-1 font-semibold">Update failed</div>
            <div>{error}</div>
          </div>
        ) : done ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-700 dark:text-emerald-200">
            <div className="mb-1 font-semibold">Updated</div>
            <div className="text-xs text-emerald-700 dark:text-emerald-300/80">
              {device.name} now reports v{confirmedVersion}.
            </div>
          </div>
        ) : requested ? (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 text-sm text-amber-800 dark:text-amber-200">
            <div className="mb-1 flex items-center gap-2 font-semibold">
              <span className="relative flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400 opacity-60" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-amber-400" />
              </span>
              Update queued
            </div>
            <div className="text-xs text-amber-800/80 dark:text-amber-300/80">
              {device.name} isn&apos;t reachable from this browser right now, so the update is
              waiting on the device instead. It will install the next time the box checks in —
              typically within a minute of coming back online. You can close this.
            </div>
            {unreachableReason ? (
              <div className="mt-2 border-t border-amber-500/20 pt-2 text-[10px] text-amber-800/70 dark:text-amber-300/60">
                Couldn&apos;t connect: {unreachableReason}
              </div>
            ) : null}
          </div>
        ) : (
          <>
            {(() => {
              // Step model — every phase emitted by the agent maps to
              // one of these. The progress bar shows ordinal/total
              // and the headline reads from STEP_LABELS so the user
              // understands what is happening right now (vs. "phase:
              // download" which is technical jargon).
              const STEPS: Array<{ phase: string; label: string }> = [
                { phase: "connect",        label: `Connecting to ${device.name}` },
                { phase: "queued",         label: "Preparing" },
                { phase: "fetch_release",  label: "Checking GitHub for the new version" },
                { phase: "check",          label: "Found a new version" },
                { phase: "download",       label: "Downloading the new binary" },
                { phase: "extract",        label: "Unpacking" },
                { phase: "replace",        label: "Replacing the running binary" },
                { phase: "restart",        label: "Restarting" },
                { phase: "ready",          label: "Ready" },
              ];
              const idx = Math.max(0, STEPS.findIndex((s) => s.phase === phase));
              const step = STEPS[idx] || STEPS[0];
              const total = STEPS.length;
              // Progress fraction: when on download phase with known
              // byte total, blend in the byte percent; otherwise step
              // index / total.
              // Connecting has no measurable fraction — we're racing a
              // relay/tunnel/LAN ladder with no idea which will win or
              // when. Drawing "step 1 of 9" as 11% would be a number we
              // made up; the sliding bar is the honest rendering.
              const indeterminate = phase === "connect";
              let pct = ((idx + 1) / total) * 100;
              if (phase === "download" && downloadBytes && downloadBytes.total > 0) {
                const dlPct = Math.max(0, Math.min(100, (downloadBytes.read * 100) / downloadBytes.total));
                // Steps 1..idx are "done" (idx/total of the bar);
                // download fills the slot between idx/total and (idx+1)/total.
                pct = (idx / total + (dlPct / 100) / total) * 100;
              }
              const dotClass =
                phase === "error"
                  ? "bg-red-400"
                  : phase === "restarting" || phase === "restart"
                  ? "bg-amber-400 animate-pulse"
                  : phase === "connect"
                  ? "bg-sky-400 animate-pulse"
                  : "bg-indigo-400 animate-pulse";
              const subtitle = (() => {
                if (phase === "connect") {
                  // We have not reached the box yet. Say that, rather
                  // than the old copy ("Asking <box> to start the
                  // update"), which claimed a conversation that hadn't
                  // started — so a slow or failing relay looked like a
                  // stuck update instead of a connection still being
                  // negotiated.
                  const dots = ".".repeat((tick % 4) + 1);
                  return `Finding a route to ${device.name}${dots}`;
                }
                if (phase === "queued") {
                  // Connected, POST sent, but the agent's first progress
                  // event hasn't landed yet. Use the tick spinner so the
                  // user sees motion.
                  const dots = ".".repeat((tick % 4) + 1);
                  return `Asking ${device.name} to start the update${dots}`;
                }
                if (phase === "download" && downloadBytes) {
                  return downloadBytes.total > 0
                    ? `${formatBytes(downloadBytes.read)} of ${formatBytes(downloadBytes.total)} (${Math.round((downloadBytes.read * 100) / downloadBytes.total)}%)`
                    : `${formatBytes(downloadBytes.read)} downloaded`;
                }
                if (phase === "restart" || phase === "restarting") {
                  return "Waiting for the agent to come back on the new version";
                }
                return null;
              })();
              return (
                <>
                  <div className="mb-2 flex items-center gap-2 text-[12px] text-surface-200">
                    <span className={`inline-block h-2 w-2 rounded-full ${dotClass}`} />
                    <span className="font-medium">{step.label}</span>
                    <span className="ml-auto text-[10px] text-surface-500">step {Math.min(idx + 1, total)} of {total}</span>
                  </div>
                  <div className="relative mb-2 h-2 w-full overflow-hidden rounded-full bg-surface-800">
                    {indeterminate ? (
                      // No route to the box yet, so no honest fraction to
                      // draw. Reuse the dashboard's existing indeterminate
                      // treatment (see PreviewPane) — a sliding gradient
                      // says "working" without inventing a percentage.
                      <div className="absolute inset-y-0 left-0 h-full w-1/4 animate-[slide_1.6s_ease-in-out_infinite] bg-gradient-to-r from-transparent via-indigo-400 to-transparent" />
                    ) : (
                      <div
                        className={`h-full ${phase === "error" ? "bg-red-500" : "bg-indigo-500"} transition-all duration-300 ease-out`}
                        style={{ width: `${Math.max(2, pct)}%` }}
                      />
                    )}
                  </div>
                  {subtitle ? (
                    <p className="mb-2 text-[11px] text-surface-400">{subtitle}</p>
                  ) : null}
                  <pre className="max-h-48 overflow-auto rounded-lg border border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-400 whitespace-pre-wrap">
                    {lines.length === 0
                      ? `[${phase}] ${step.label}…`
                      : lines.map((l) => `[${l.phase}] ${l.text}`).join("\n")}
                  </pre>
                  <p className="mt-2 text-[10px] text-surface-600">
                    The agent will restart itself once the new binary is in place. This dialog reconnects to /info to confirm the new version.
                  </p>
                </>
              );
            })()}
          </>
        )}
      </div>
    </div>
  );
}

// DeviceProjectsRail — folded-by-default summary on the device card.
// Mirrors the "Other available agents (N)" pattern: small `<details>`
// with a count, expanding to a chip rail. Each chip surfaces a stack
// badge, a git-configured marker, and a monorepo-app marker; clicking
// any chip jumps the user into the Details panel where the full per-
// project view lives. Skipped entirely when the device is offline /
// guest / has zero projects so the card stays compact for those rows.
function DeviceProjectsRail({
  device,
  token,
  onShowDetails,
}: {
  device: Device;
  token: string | null;
  onShowDetails?: () => void;
}) {
  const { projects, error, errorDetails, loading } = useDeviceProjects(device, !device.isGuest, token);
  const classifiedFailure: ClassifiedFailure | null = error
    ? classifyFetchError({
        error: errorDetails?.message ?? error,
        response: errorDetails?.status ? { status: errorDetails.status } : null,
        path: errorDetails?.path,
        url: errorDetails?.url,
      })
    : null;

  // Three render modes — keep the disclosure visible in all of them
  // so the user always sees the affordance, even when /projects has
  // not arrived yet (loading) or the agent transport is wedged
  // (error). Empty-but-loaded is the only case we hide for, since a
  // "(0)" chip is just visual noise for machines with no detected
  // projects.
  const ready = !loading && !error && Array.isArray(projects);
  if (ready && (projects?.length ?? 0) === 0) return null;

  // Header label uses git-configured count when known, total
  // otherwise. "Git projects" matches the dashboard's existing
  // terminology (the "Git" tab) and signals that these are working
  // trees, not arbitrary directories.
  const gitCount = ready ? projects!.filter((p) => !!(p.remote && p.remote.length > 0)).length : null;
  const totalCount = ready ? projects!.length : null;
  const headerCount = ready
    ? gitCount === totalCount
      ? `(${totalCount})`
      : `(${gitCount} / ${totalCount})`
    : loading
      ? "(…)"
      : "(— unavailable)";

  return (
    <details className="mt-1.5 rounded-lg border border-slate-200 bg-slate-50/70 dark:border-surface-800 dark:bg-surface-900/30">
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[11px] text-slate-600 hover:text-slate-900 dark:text-surface-400 dark:hover:text-surface-200">
        <span>Git projects</span>
        <span className="text-[10px] text-slate-500 dark:text-surface-500">{headerCount}</span>
      </summary>
      <div className="flex flex-wrap items-center gap-1.5 border-t border-slate-200 px-3 py-2 dark:border-surface-800/60">
        {loading ? (
          <span className="text-[10px] text-slate-500 dark:text-surface-500">Loading project list from agent…</span>
        ) : classifiedFailure ? (
          <div className="text-[10px] text-slate-500 dark:text-surface-500">
            <div>
              <span className="font-semibold text-amber-700 dark:text-amber-300">
                {classifiedFailure.label}
              </span>
              {" — "}
              <span>{classifiedFailure.detail}</span>
            </div>
            {classifiedFailure.suggestedAction ? (
              <div className="mt-0.5 text-slate-400 dark:text-surface-600">
                {classifiedFailure.suggestedAction}
              </div>
            ) : null}
            {classifiedFailure.raw && classifiedFailure.raw !== classifiedFailure.label ? (
              <div className="mt-0.5 font-mono text-[9px] text-slate-400 dark:text-surface-700">
                (raw: {classifiedFailure.raw})
              </div>
            ) : null}
            <div className="mt-0.5">
              <BackoffHint deviceId={device.id} kind="projects" />
            </div>
          </div>
        ) : (
          (projects || []).map((p) => {
            const stack = (p.framework || "").toUpperCase();
            const hasGit = !!(p.remote && p.remote.length > 0);
            const isMonorepoApp = !!(p.monorepoApp && p.monorepoApp.length > 0);
            const tip = [
              p.path,
              stack && `stack: ${stack.toLowerCase()}`,
              p.branch && `branch: ${p.branch}`,
              hasGit ? `git: ${p.remote}` : "no git remote",
              isMonorepoApp && `monorepo app: ${p.monorepoApp}`,
            ]
              .filter(Boolean)
              .join(" · ");
            return (
              <button
                key={`pr:${device.id}:${p.name}`}
                type="button"
                onClick={onShowDetails}
                className="inline-flex items-center gap-1 rounded border border-emerald-300 bg-emerald-50 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-emerald-800 hover:bg-emerald-100 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-200 dark:hover:bg-emerald-500/20"
                title={tip || undefined}
              >
                <span className="text-emerald-900 dark:text-emerald-100">{p.name}</span>
                {stack ? (
                  <span className="rounded bg-emerald-100 px-1 text-[9px] font-normal normal-case text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300/80">
                    {stack}
                  </span>
                ) : null}
                {/* Git-configured marker. The little link glyph means
                    the project has a configured `origin` remote and is
                    pushable; absence means the dir is on disk but has
                    no git history yet. */}
                {hasGit ? (
                  <span className="text-emerald-700 dark:text-emerald-300/80" title={`git remote: ${p.remote}`}>⌬</span>
                ) : (
                  <span className="text-slate-400 dark:text-surface-600" title="no git remote configured">∅</span>
                )}
                {/* Monorepo-app marker. Filled when the agent's
                    workspace manifest declares this project as one app
                    inside a multi-app yaver.workspace.yaml — distinct
                    from a top-level repo. */}
                {isMonorepoApp ? (
                  <span className="text-amber-700 dark:text-amber-300/80" title={`monorepo app · root ${p.monorepoRoot}`}>◫</span>
                ) : null}
              </button>
            );
          })
        )}
      </div>
    </details>
  );
}

// Every per-device action except the one CTA the card is for
// (Open Workspace). They used to sit on the card as ~9 competing
// buttons across two rows; the card is a status surface first, so
// they live behind one "⋯" affordance now. Ping keeps its hook here
// rather than in the panel so a result survives closing the menu.
function DeviceActionsMenu({
  device,
  token,
  sshHref,
  sshCommand,
  isPrimary,
  isSecondary,
  detailsOpen,
  rescueOpen,
  onSetPrimary,
  onSetSecondary,
  onRecycle,
  onRescue,
  onShell,
  onCodingAgent,
  onToggleDetails,
  onLeftShare,
}: {
  device: Device;
  token: string | null | undefined;
  sshHref: string | null;
  sshCommand: string;
  isPrimary: boolean;
  isSecondary: boolean;
  detailsOpen: boolean;
  rescueOpen: boolean;
  onSetPrimary: () => void;
  onSetSecondary: () => void;
  onRecycle: () => void;
  onRescue: () => void;
  onShell: () => void;
  onCodingAgent: () => void;
  onToggleDetails: () => void;
  onLeftShare: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [leaveConfirming, setLeaveConfirming] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [leaveError, setLeaveError] = useState<string | null>(null);
  const { pingState, ping } = useDevicePing(device, token);
  const pingFailure = pingState.ok === false ? classifyPingFailure(pingState) : null;
  const canManage = !device.isGuest && !!token;

  async function doLeave() {
    if (!token) return;
    setLeaving(true);
    setLeaveError(null);
    try {
      await leaveSharedAccess(token, {
        hostUserId: device.hostUserIdString,
        hostEmail: device.hostEmail,
      });
      setOpen(false);
      setLeaveConfirming(false);
      onLeftShare();
    } catch (e: any) {
      setLeaveError(e?.message || "Failed to remove access");
    } finally {
      setLeaving(false);
    }
  }

  const itemClass =
    "flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-[12px] text-slate-700 hover:bg-slate-100 dark:text-surface-200 dark:hover:bg-surface-800";
  const hintClass = "shrink-0 text-[10px] text-slate-400 dark:text-surface-500";

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Actions for ${device.name}`}
        title="Actions — shell, ping, SSH, rescue, details"
        className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-slate-300 bg-white text-slate-600 hover:border-slate-400 hover:bg-slate-50 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.82)] dark:text-surface-300 dark:hover:border-surface-600 dark:hover:bg-[rgba(31,33,41,0.94)]"
      >
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
          <circle cx="5" cy="12" r="1.6" />
          <circle cx="12" cy="12" r="1.6" />
          <circle cx="19" cy="12" r="1.6" />
        </svg>
      </button>
      {open ? (
        <>
          <button
            type="button"
            aria-label="Close menu"
            onClick={() => setOpen(false)}
            className="fixed inset-0 z-30 cursor-default"
          />
          <div
            role="menu"
            className="absolute right-0 top-full z-40 mt-1 min-w-[228px] overflow-hidden rounded-md border border-slate-200 bg-white py-1 shadow-lg dark:border-surface-700 dark:bg-surface-900"
          >
            <button className={itemClass} onClick={() => { onToggleDetails(); setOpen(false); }}>
              <span>{detailsOpen ? "Hide details" : "Details"}</span>
              <span className={hintClass}>runtime · network</span>
            </button>
            <button className={itemClass} onClick={() => { onShell(); setOpen(false); }}>
              <span>Shell</span>
              <span className={hintClass}>PTY in browser</span>
            </button>
            <button className={itemClass} onClick={() => { onCodingAgent(); setOpen(false); }}>
              <span>Coding agent…</span>
              <span className={hintClass}>runner · model</span>
            </button>
            <button
              className={itemClass}
              disabled={pingState.pinging}
              onClick={() => void ping()}
              title={pingFailure ? pingFailure.title : "Probe /health via relay first, then direct host"}
            >
              <span>Ping</span>
              <span
                className={
                  pingState.ok === true
                    ? "shrink-0 text-[10px] text-emerald-600 dark:text-emerald-400"
                    : pingFailure
                      ? "shrink-0 text-[10px] text-amber-600 dark:text-amber-400"
                      : hintClass
                }
              >
                {pingState.pinging
                  ? "pinging…"
                  : pingState.ok === true
                    ? `${pingState.rttMs}ms`
                    : pingFailure
                      ? pingFailure.label
                      : "reachability"}
              </span>
            </button>
            <div className="my-1 border-t border-slate-200 dark:border-surface-800" />
            <a
              role="menuitem"
              href={sshHref ?? undefined}
              onClick={(e) => {
                if (!sshHref) {
                  e.preventDefault();
                  return;
                }
                setOpen(false);
              }}
              aria-disabled={!sshHref}
              className={
                sshHref
                  ? itemClass
                  : `${itemClass} cursor-not-allowed opacity-50 hover:bg-transparent dark:hover:bg-transparent`
              }
              title={sshHref ? "Open your system SSH handler for this machine" : "No direct SSH host advertised by this device"}
            >
              <span>Open SSH</span>
              <span className={hintClass}>{sshHref ? "system handler" : "no host"}</span>
            </a>
            <button
              className={itemClass}
              title={`Copy ${sshCommand}`}
              onClick={() => {
                void (async () => {
                  try {
                    await navigator.clipboard.writeText(sshCommand);
                    setCopied(true);
                    window.setTimeout(() => setCopied(false), 2000);
                  } catch (e: any) {
                    alert(`Copy failed: ${e?.message || e}`);
                  }
                })();
              }}
            >
              <span>Copy SSH command</span>
              <span className={copied ? "shrink-0 text-[10px] text-emerald-600 dark:text-emerald-400" : hintClass}>
                {copied ? "copied" : "clipboard"}
              </span>
            </button>
            {!device.isGuest ? (
              <button className={itemClass} onClick={() => { onRescue(); setOpen(false); }}>
                <span>{rescueOpen ? "Hide rescue" : "Rescue"}</span>
                <span className={hintClass}>wedged agent</span>
              </button>
            ) : null}
            {/* Guest-side exit. The host's own revoke lives in Guests; this is
                the mirror for the receiving end — drop a share you never
                wanted without having to ask the host to pull it. */}
            {device.isGuest && token ? (
              <>
                <div className="my-1 border-t border-slate-200 dark:border-surface-800" />
                {leaveConfirming ? (
                  <div className="px-3 py-2">
                    <p className="text-[11px] leading-snug text-slate-600 dark:text-surface-300">
                      Remove your access to{" "}
                      <span className="font-semibold">{device.hostName || "this host"}</span>
                      &rsquo;s shared machines? They can share again later, and you can accept again.
                    </p>
                    {leaveError ? (
                      <p className="mt-1 text-[10px] text-rose-600 dark:text-rose-400">{leaveError}</p>
                    ) : null}
                    <div className="mt-2 flex items-center gap-2">
                      <button
                        type="button"
                        disabled={leaving}
                        onClick={() => void doLeave()}
                        className="rounded border border-rose-300 bg-rose-50 px-2 py-1 text-[11px] font-semibold text-rose-700 hover:bg-rose-100 disabled:opacity-50 dark:border-rose-500/40 dark:bg-rose-500/10 dark:text-rose-300 dark:hover:bg-rose-500/20"
                      >
                        {leaving ? "Removing…" : "Yes, remove"}
                      </button>
                      <button
                        type="button"
                        disabled={leaving}
                        onClick={() => { setLeaveConfirming(false); setLeaveError(null); }}
                        className="rounded border border-slate-300 px-2 py-1 text-[11px] text-slate-600 hover:bg-slate-100 disabled:opacity-50 dark:border-surface-700 dark:text-surface-300 dark:hover:bg-surface-800"
                      >
                        Cancel
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    className={`${itemClass} text-rose-700 hover:bg-rose-50 dark:text-rose-300 dark:hover:bg-rose-500/10`}
                    onClick={() => setLeaveConfirming(true)}
                    title={`Remove your own access to ${device.hostName || "this host"}'s shared machines. Reversible — they can share again.`}
                  >
                    <span>Remove my access</span>
                    <span className={hintClass}>leave share</span>
                  </button>
                )}
              </>
            ) : null}
            {canManage ? (
              <>
                <div className="my-1 border-t border-slate-200 dark:border-surface-800" />
                <button className={itemClass} onClick={() => { onSetPrimary(); setOpen(false); }}>
                  <span>{isPrimary ? "Unset primary" : "Set primary"}</span>
                  <span className={hintClass}>★</span>
                </button>
                {!isPrimary ? (
                  <button className={itemClass} onClick={() => { onSetSecondary(); setOpen(false); }}>
                    <span>{isSecondary ? "Unset secondary" : "Set secondary"}</span>
                    <span className={hintClass}>fallback</span>
                  </button>
                ) : null}
                <button
                  className={`${itemClass} text-amber-700 hover:bg-amber-50 dark:text-amber-300 dark:hover:bg-amber-500/10`}
                  onClick={() => { onRecycle(); setOpen(false); }}
                  title="Recycle this box: provision a fresh box, health-check, then snapshot+delete the old one (dry-run first)"
                >
                  <span>♻ Recycle box</span>
                  <span className={hintClass}>dry-run first</span>
                </button>
              </>
            ) : null}
          </div>
        </>
      ) : null}
    </div>
  );
}

function ConnectionSection({ device }: { device: Device }) {
  const t = transportFor(device);
  const relayHealth = useRelayHealth(t.primary === "yaver-public-relay" || t.primary === "self-hosted-relay" ? t.url : null);

  const lanIps = (device.localIps || []).filter(Boolean);
  const tailscaleIp = lanIps.find((ip) => /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./.test(ip));
  const wslIp = lanIps.find((ip) => /^172\.(1[6-9]|2\d|3[0-1])\./.test(ip));
  const privateLanIps = lanIps.filter(
    (ip) => /^(10\.|192\.168\.)/.test(ip) && ip !== tailscaleIp,
  );

  return (
    <div className="mb-4 rounded-md border border-surface-800 bg-surface-950/30 p-3">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">Connection</div>
        <span className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${transportToneClasses(t.tone)}`}>
          {t.label}
        </span>
      </div>
      <div className="grid gap-x-6 gap-y-1 text-xs md:grid-cols-2">
        {/* Primary transport detail */}
        <div className="flex items-start justify-between gap-3 py-1">
          <span className="text-surface-500">Active path</span>
          <span className="text-right text-surface-200">{t.detail}</span>
        </div>
        {t.url ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">URL</span>
            <span className="break-all text-right font-mono text-[11px] text-surface-200">{t.url}</span>
          </div>
        ) : null}
        {/* Relay version when relay-routed */}
        {(t.primary === "yaver-public-relay" || t.primary === "self-hosted-relay") ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">Relay version</span>
            <span className="text-right text-surface-200">
              {relayHealth?.version ? (
                <span className="inline-flex items-center gap-1">
                  <span>v{relayHealth.version}</span>
                  {typeof relayHealth.tunnels === "number" ? (
                    <span className="rounded border border-surface-700 bg-surface-800/40 px-1 text-[10px] text-surface-400">
                      {relayHealth.tunnels} tunnel{relayHealth.tunnels === 1 ? "" : "s"}
                    </span>
                  ) : null}
                </span>
              ) : (
                <span className="text-surface-600">probing…</span>
              )}
            </span>
          </div>
        ) : null}
        {/* Tunnel URL row when relevant */}
        {device.tunnelUrl ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">Tunnel URL</span>
            <span className="break-all text-right font-mono text-[11px] text-surface-200">{device.tunnelUrl}</span>
          </div>
        ) : null}
        {/* Tailscale IP if present */}
        {tailscaleIp ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">Tailscale IP</span>
            <span className="text-right font-mono text-surface-200">{tailscaleIp}:{device.port ?? 18080}</span>
          </div>
        ) : null}
        {/* WSL2 NAT IP if present */}
        {wslIp ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">WSL2 NAT IP</span>
            <span className="text-right font-mono text-surface-200">{wslIp}:{device.port ?? 18080}</span>
          </div>
        ) : null}
        {/* Private LAN IPs */}
        {privateLanIps.length ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">LAN IPs</span>
            <span className="text-right font-mono text-surface-200">{privateLanIps.join(", ")}</span>
          </div>
        ) : null}
        {/* Public endpoints */}
        {(device.publicEndpoints || []).length ? (
          <div className="flex items-start justify-between gap-3 py-1">
            <span className="text-surface-500">Public endpoints</span>
            <span className="break-all text-right font-mono text-[11px] text-surface-200">
              {(device.publicEndpoints || []).join(", ")}
            </span>
          </div>
        ) : null}
        {/* Direct host:port */}
        <div className="flex items-start justify-between gap-3 py-1">
          <span className="text-surface-500">Reported host</span>
          <span className="text-right font-mono text-surface-200">{device.host}:{device.port ?? 18080}</span>
        </div>
      </div>
    </div>
  );
}

// FactoryResetAuthButton — rendered on every owner-scope device card.
// Sends POST /auth/factory-reset through the relay using the user's
// own bearer; the agent verifies ownership via Convex round-trip
// (see desktop/agent/auth_factory_reset_http.go) so it works EVEN
// when the agent's local auth_token belongs to a different user
// (the bug this is fixing). Hidden for guests — they can't reset
// the host's auth.
function FactoryResetAuthButton({ device }: { device: Device }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const onClick = async () => {
    if (busy) return;
    const ok = window.confirm(
      `Factory-reset auth on "${device.name}"?\n\n` +
      `The agent will exit and restart in bootstrap mode. You'll re-pair it from this dashboard.\n\n` +
      `Use this when:\n` +
      `  • the agent rejects your session ("token belongs to a different user")\n` +
      `  • AUTH / Recover Auth doesn't fix it\n` +
      `  • the box was paired to someone else and you've taken it over\n\n` +
      `This does NOT delete your projects, vault, or workspace files.`
    );
    if (!ok) return;
    setBusy(true);
    setMsg(null);
    try {
      const res = await agentClient.factoryResetDeviceAuth(device.id);
      if (res.ok) {
        setMsg("✓ reset triggered — re-pair when the agent comes back (~10s)");
        setTimeout(() => setMsg(null), 8000);
      } else {
        setMsg(`✗ ${res.error}`);
      }
    } catch (e: unknown) {
      setMsg(`✗ ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };
  return (
    <span className="inline-flex items-center gap-2">
      <button
        onClick={onClick}
        disabled={busy}
        className="rounded-md border border-rose-500/40 bg-rose-500/10 px-2.5 py-1 text-[11px] font-medium text-rose-700 dark:text-rose-200 hover:border-rose-400 hover:text-rose-800 dark:hover:text-rose-100 disabled:opacity-50"
        title="Wipe the agent's local auth_token + device_id and put it back into bootstrap (pairing) mode. Use when the box has someone else's session and AUTH/recover can't fix it."
      >
        {busy ? "Resetting..." : "Reset Auth"}
      </button>
      {msg && (
        <span className={`text-[10px] ${msg.startsWith("✓") ? "text-emerald-700 dark:text-emerald-300" : "text-rose-700 dark:text-rose-300"}`}>
          {msg}
        </span>
      )}
    </span>
  );
}

function useRelayHealth(relayUrl: string | null | undefined) {
  const [state, setState] = useState<{ version?: string; tunnels?: number; activeDevices?: number } | null>(null);
  useEffect(() => {
    if (!relayUrl) { setState(null); return; }
    let cancelled = false;
    const ac = new AbortController();
    void fetchRelayHealth(relayUrl, ac.signal).then((h) => {
      if (!cancelled) setState(h);
    });
    return () => { cancelled = true; ac.abort(); };
  }, [relayUrl]);
  return state;
}

function DeviceDetailsPanel({ device, token }: { device: Device; token: string | null }) {
  const [showNetcap, setShowNetcap] = useState(false);
  const { info, error, errorDetails: runtimeErrorDetails, loading } = useDeviceRuntimeInfo(device, true, token);
  const runtimeFailure: ClassifiedFailure | null = error
    ? classifyFetchError({
        error: runtimeErrorDetails?.message ?? error,
        response: runtimeErrorDetails?.status ? { status: runtimeErrorDetails.status } : null,
        path: runtimeErrorDetails?.path,
        url: runtimeErrorDetails?.url,
      })
    : null;
  const { status: updateStatus, error: updateError, loading: updateLoading, updating, trigger: triggerUpdate } =
    useDeviceAgentUpdate(device, true, token);
  // Phones (iOS + Android) reachable from this agent over WiFi via xcrun
  // devicectl + adb. Lives entirely on the agent; not persisted to Convex.
  // Only relevant for desktop / mobile-dev machines, but cheap enough to
  // probe on every device — the agent returns count=0 for servers.
  const { data: wirelessPhones, error: wirelessPhonesError, loading: wirelessPhonesLoading } =
    useAgentWirelessDevices(device, true, token);
  const effectiveInfo = (info || device.probeInfo || null) as DeviceRuntimeInfo | null;
  const { pingState, ping } = useDevicePing(device, token);
  const pingFailure = pingState.ok === false ? classifyPingFailure(pingState) : null;
  // Guests list /projects under a host-shared-scope allowlist but we
  // never want to display raw owner workdir paths to a guest — cap it
  // to owner sessions for now.
  const { projects: liveProjects, error: projectsError, errorDetails: projectsErrorDetails, loading: projectsLoading } =
    useDeviceProjects(device, !device.isGuest, token);
  const liveProjectsFailure: ClassifiedFailure | null = projectsError
    ? classifyFetchError({
        error: projectsErrorDetails?.message ?? projectsError,
        response: projectsErrorDetails?.status ? { status: projectsErrorDetails.status } : null,
        path: projectsErrorDetails?.path,
        url: projectsErrorDetails?.url,
      })
    : null;
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
  const currentVersion = typeof effectiveInfo?.version === "string" && effectiveInfo.version.trim()
    ? effectiveInfo.version
    : device.agentVersion;
  // Prefer the agent's live /info.hardware (always current, even on a
  // fresh restart) and fall back to device.hardwareProfile (Convex-
  // synced, may be stale or empty if the agent hasn't pushed yet).
  // The Convex-only path made the Details panel render "—" for every
  // hardware row whenever the heartbeat hadn't shipped a profile, even
  // though /info has the same data live.
  const liveHardware = (effectiveInfo as unknown as { hardware?: typeof device.hardwareProfile })?.hardware;
  const hardware = liveHardware ?? device.hardwareProfile;
  const hardwareOS = [hardware?.os || device.platform, hardware?.osVersion].filter(Boolean).join(" ");
  const iosSimulators = formatCapabilityList(hardware?.iosSimulators);
  const androidEmulators = formatCapabilityList(hardware?.androidEmulators);
  const latestVersion = updateStatus?.latestVersion;
  const outdated = updateStatus?.updateAvailable || isVersionOutdated(currentVersion, latestVersion);

  // Defensive coercion: agent /info shapes drift between versions
  // (e.g. autoStart used to be a boolean and became {enabled, type}
  // in v1.99.x). Stuffing an unexpected object into a JSX child
  // crashes the whole tree with "Objects are not valid as a React
  // child" — taking down the entire dashboard for the user, not
  // just the row. Coerce anything non-primitive / non-element to
  // a readable string here so the panel keeps rendering even when
  // the agent is on a different version than the dashboard.
  const safeValue = (v: unknown): React.ReactNode => {
    if (v == null) return null;
    if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") {
      return String(v);
    }
    // React elements are objects but pass `$$typeof` — let them through.
    if (typeof v === "object" && (v as { $$typeof?: symbol }).$$typeof) {
      return v as React.ReactNode;
    }
    if (typeof v === "object") {
      try { return JSON.stringify(v); } catch { return "[unserialisable]"; }
    }
    return String(v);
  };
  const row = (label: string, value: unknown) => (
    <div className="flex items-start justify-between gap-3 py-1 text-xs">
      <span className="text-surface-500">{label}</span>
      <span className="text-right text-surface-200">{safeValue(value) || sysUnknown}</span>
    </div>
  );

  return (
    <div className="mt-3 rounded-lg border border-surface-800 bg-surface-900/40 p-3">
      {showNetcap && (
        <NetCaptureModal device={device} token={token} onClose={() => setShowNetcap(false)} />
      )}
      <div className="mb-3 flex flex-wrap justify-end gap-2">
        {!device.isGuest && (
          <button
            onClick={() => setShowNetcap(true)}
            className="rounded-md border border-surface-700 px-2.5 py-1 text-[11px] font-semibold text-surface-200 hover:border-surface-500 hover:text-surface-50"
            title="Capture and deep-analyze network + serial traffic on this machine (PLC/Modbus/S7/OPC-UA/SQL/HTTP, RS232/RS485). Requires the agent to run with --netcapture."
          >
            Network / Wire Monitor
          </button>
        )}
        {outdated && latestVersion ? (
          <button
            onClick={() => {
              void triggerUpdate()
                .then((res) => {
                  if (res?.message) alert(res.message);
                })
                .catch((e: any) => alert(`Failed to trigger update: ${e?.message ?? e}`));
            }}
            disabled={updating || updateStatus?.updating}
            className="rounded-md border px-2.5 py-1 text-[11px] font-semibold disabled:opacity-50 border-amber-400 bg-amber-100 text-amber-800 hover:bg-amber-200 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:border-amber-400 dark:hover:text-amber-100"
            title={`Update this machine from ${currentVersion || "current"} to ${latestVersion}. The agent may restart and disconnect briefly.`}
          >
            {updating || updateStatus?.updating ? "Updating..." : `Update to v${String(latestVersion).replace(/^v/i, "")}`}
          </button>
        ) : null}
        <button
          onClick={() => void ping()}
          disabled={pingState.pinging}
          className="rounded-md border border-surface-700 bg-surface-950 px-2.5 py-1 text-[11px] font-medium text-surface-300 hover:border-surface-600 hover:text-surface-100 disabled:opacity-50"
          title={pingFailure ? pingFailure.title : "Probe /health over relay, tunnel, or direct host"}
        >
          {pingState.pinging
            ? "Pinging..."
            : pingState.ok === true
              ? `${pingState.rttMs}ms`
              : pingFailure
                ? pingFailure.label
                : "Ping"}
        </button>
        {!device.isGuest ? (
          <FactoryResetAuthButton device={device} />
        ) : null}
      </div>
      {pingFailure ? (
        <div className="mb-3 text-right text-[11px] text-surface-500">{pingFailure.title}</div>
      ) : null}
      <ConnectionSection device={device} />
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
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Hardware</div>
          {row("OS", hardwareOS || null)}
          {row("CPU", hardware?.cpu ? <span className="font-mono text-[11px]">{hardware.cpu}</span> : null)}
          {row("RAM", formatMemoryMb(hardware?.ramMb))}
          {/* Disk: capacity from the (static) hardware profile, live free/used
              from the storage gauge the agent refreshes every heartbeat. */}
          {row("Disk", formatDiskUsage(device.storage, hardware?.diskTotalGb))}
          {row("GPU", hardware?.gpu ? <span className="font-mono text-[11px]">{hardware.gpu}</span> : null)}
          {row("VRAM", formatMemoryMb(hardware?.vramMb))}
          {row("Cores", typeof hardware?.numCores === "number" && hardware.numCores > 0 ? String(hardware.numCores) : null)}
          {row("Arch", hardware?.arch ? <span className="font-mono">{hardware.arch}</span> : null)}
          {row("iOS simulators", iosSimulators)}
          {row("Android emulators", androidEmulators)}
        </div>
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Runtime</div>
          {(() => {
            const lifecycleState = String(device.probeInfo?.lifecycle?.state || device.probeInfo?.lifecycleState || deriveDeviceLifecycleState(device));
            const lifecycle = device.probeInfo?.lifecycle;
            const authLabel =
              lifecycleState === "bootstrap"
                ? lifecycle?.requiresFirstPair
                  ? "Bootstrap (first pair required)"
                  : lifecycle?.supportsOwnerClaim
                    ? lifecycle?.ownerClaimReady
                      ? "Bootstrap (reclaim ready)"
                      : "Bootstrap (reclaim rotating)"
                    : "Bootstrap"
                : lifecycleState === "yaver-auth-expired"
                  ? "Expired"
                  : device.workspaceLive
                    ? "Authenticated workspace"
                    : "Authenticated";
            return (
              <>
          {row("Status", deriveDeviceLifecycleState(device).replace(/-/g, " "))}
          {row("Auth", authLabel)}
          {/* Agent-reported usable + recoverable bits. Surfacing them
              instead of letting them rot turns a regression into
              something a user can spot — e.g. lifecycle.recoverable=false
              on an "auth-expired" row indicates the agent has lost the
              hooks needed for /auth/recover and should be re-paired. */}
          {row("Agent reports usable", typeof lifecycle?.usable === "boolean" ? (lifecycle.usable ? "yes" : "no") : null)}
          {row("Agent reports recoverable", typeof lifecycle?.recoverable === "boolean" ? (lifecycle.recoverable ? "yes" : "no") : null)}
          {row("Agent mode", typeof effectiveInfo?.mode === "string" ? effectiveInfo.mode : null)}
          {row("Live signal", device.lastTunnelEvent?.at ? `${device.lastTunnelEvent.online ? "relay-online" : "relay-offline"} (${formatLastSeen(new Date(device.lastTunnelEvent.at).toISOString())})` : null)}
          {row("Peer bus", device.peerState ? `${device.peerState}${device.peerLastSeen ? ` (${formatLastSeen(device.peerLastSeen)})` : ""}` : null)}
          {row("Authenticated probe", device.probeState ? `${device.probeState}${device.probePath ? ` via ${device.probePath}` : ""}${device.probeCheckedAt ? ` (${formatLastSeen(device.probeCheckedAt)})` : ""}` : null)}
          {row("Reachability", deviceReachabilitySummary(device))}
          {device.ghost ? row("Identity", "missing hwid + publicKey — re-pair recommended") : null}
              </>
            );
          })()}
          {row("Last agent signal", device.lastSeen ? `${formatLastSeen(device.lastSeen)} (${device.lastSeen})` : null)}
          {row(
            "Yaver version",
            <span className="inline-flex flex-wrap items-center justify-end gap-2">
              <span>{currentVersion || <span className="text-surface-600">no version info</span>}</span>
              {latestVersion ? (
                <span className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold ${
                  outdated
                    ? "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                    : "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
                }`}>
                  {outdated ? `latest v${String(latestVersion).replace(/^v/i, "")} available` : `latest v${String(latestVersion).replace(/^v/i, "")}`}
                </span>
              ) : updateLoading ? (
                <span className="text-surface-500">checking latest…</span>
              ) : null}
            </span>,
          )}
          {row("Auto-update", updateStatus ? (updateStatus.autoUpdateEnabled ? "Enabled" : "Disabled") : null)}
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
      {/* Collapsed by default and lazy — the scan shells out to `du` across the
          box's home dir, so it only runs when someone actually opens the fold. */}
      <DeviceStorageFold device={device} token={token} />
      {(allRunners.length || allSharedRunners.length) ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Agents / Runners
          </div>
          <div className="flex flex-wrap gap-1.5">
            {(allRunners.length ? allRunners : allSharedRunners).map((r) => (
              <span key={`rr:${device.id}:${r}`} className="rounded border border-violet-500/40 bg-violet-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-violet-700 dark:text-violet-200">
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
              <span key={`gg:${device.id}:${g}`} className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-sky-700 dark:text-sky-200">
                {g}
              </span>
            ))}
          </div>
        </div>
      ) : null}
      {!device.isGuest ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Available projects
          </div>
          {liveProjects && liveProjects.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {liveProjects.map((p) => (
                <span
                  key={`avp:${device.id}:${p.name}`}
                  className="inline-flex items-center gap-1 rounded border border-emerald-500/40 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-emerald-700 dark:text-emerald-200"
                  title={[p.path, p.branch && `branch: ${p.branch}`, p.framework].filter(Boolean).join(" · ") || undefined}
                >
                  {p.name}
                  {p.framework ? (
                    <span className="text-[9px] font-normal normal-case text-emerald-700 dark:text-emerald-300/70">
                      {p.framework}
                    </span>
                  ) : null}
                </span>
              ))}
            </div>
          ) : projectsLoading ? (
            <p className="text-[11px] text-surface-500">Loading project list from agent…</p>
          ) : liveProjects && liveProjects.length === 0 ? (
            <p className="text-[11px] text-surface-500">No projects detected on this machine.</p>
          ) : liveProjectsFailure ? (
            <div className="text-[11px] text-surface-600">
              <p>
                <span className="font-semibold text-amber-700 dark:text-amber-300">{liveProjectsFailure.label}</span>
                {" — "}{liveProjectsFailure.detail}
              </p>
              {liveProjectsFailure.suggestedAction ? (
                <p className="mt-0.5 text-surface-500">{liveProjectsFailure.suggestedAction}</p>
              ) : null}
              <div className="mt-0.5"><BackoffHint deviceId={device.id} kind="projects" /></div>
            </div>
          ) : (
            <p className="text-[11px] text-surface-600">Project list unavailable — agent offline.</p>
          )}
        </div>
      ) : null}
      {device.sharedProjects?.length || device.sharesAllProjects ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Shared projects
          </div>
          {device.sharesAllProjects ? (
            <span className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-700 dark:text-amber-200">
              All projects
            </span>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {(device.sharedProjects || []).map((p) => (
                <span key={`pp:${device.id}:${p}`} className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-amber-700 dark:text-amber-200">
                  {p}
                </span>
              ))}
            </div>
          )}
        </div>
      ) : null}
      <div className="mt-3">
        <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
          WiFi-paired phones
        </div>
        {wirelessPhonesLoading && !wirelessPhones ? (
          <p className="text-[11px] text-surface-500">Probing this machine for WiFi-paired iPhones / Androids…</p>
        ) : wirelessPhones && wirelessPhones.devices.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {wirelessPhones.devices.map((d) => (
              <span
                key={`wp:${device.id}:${d.udid}`}
                className="inline-flex items-center gap-1.5 rounded border border-emerald-500/30 bg-emerald-500/5 px-1.5 py-0.5 text-[10px] tracking-wider text-emerald-700 dark:text-emerald-200"
                title={`${d.platform === "ios" ? "iPhone/iPad (xcrun devicectl)" : "Android (adb)"}\n${d.udid}${d.os ? `\nOS ${d.os}` : ""}`}
              >
                <span className="font-semibold uppercase">{d.platform}</span>
                <span className="text-emerald-800 dark:text-emerald-100">{d.name || "(unknown)"}</span>
                <span className="font-mono text-[9px] text-emerald-700 dark:text-emerald-300/70">
                  {d.udid.length > 16 ? `${d.udid.slice(0, 14)}…` : d.udid}
                </span>
              </span>
            ))}
          </div>
        ) : wirelessPhones && wirelessPhones.devices.length === 0 ? (
          <p className="text-[11px] text-surface-600">
            No WiFi-paired phones detected{wirelessPhones.hint ? ` — ${wirelessPhones.hint}` : ""}.
            {" "}Pair one with <span className="font-mono">yaver wireless detect</span> on this machine.
          </p>
        ) : wirelessPhonesError ? (
          <p className="text-[11px] text-surface-600">
            Phone list unavailable — {wirelessPhonesError}.
          </p>
        ) : null}
      </div>
      {loading ? (
        <p className="mt-3 text-[11px] text-surface-500">Loading runtime info from agent…</p>
      ) : null}
      {runtimeFailure ? (
        <div className="mt-3 rounded border border-amber-500/30 bg-amber-500/5 p-2 text-[11px] text-surface-300">
          <p>
            <span className="font-semibold text-amber-700 dark:text-amber-300">{runtimeFailure.label}</span>
            {" — "}{runtimeFailure.detail}
          </p>
          {runtimeFailure.suggestedAction ? (
            <p className="mt-0.5 text-surface-500">{runtimeFailure.suggestedAction}</p>
          ) : null}
          <div className="mt-0.5"><BackoffHint deviceId={device.id} kind="info" /></div>
          <p className="mt-1 text-surface-600">
            Showing {device.probeInfo ? "last authenticated probe + cached registry fields" : "cached registry fields only"}.
          </p>
        </div>
      ) : null}
      {updateError ? (
        <p className="mt-2 text-[11px] text-surface-600">
          Update status unavailable ({updateError}).
        </p>
      ) : null}
      <div className="mt-3 flex justify-end border-t border-surface-800/60 pt-2">
        <button
          onClick={() => hideDevice(device.id)}
          className="text-[11px] text-surface-500 hover:text-red-700 dark:hover:text-red-300"
          title="Hide this device from the list — local to this browser"
        >
          Hide this device
        </button>
      </div>
    </div>
  );
}

/**
 * Remote "Sign in" modal. Kicks off `codex login --device-auth` or
 * `claude auth login --claudeai` on the connected agent, pulls the
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
  // Claude's modern OAuth flow returns a long token the user must
  // paste back into the CLI on the remote machine. We pipe that paste
  // through the agent's /runner-auth/browser/submit-code endpoint
  // straight into the spawned `claude auth login --claudeai` stdin.
  // Codex still uses the auto-completing device-auth flow and doesn't
  // need this field — it never renders for runner=codex.
  const [authCode, setAuthCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
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
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-300">
            <div className="font-semibold mb-1">Couldn't start sign-in</div>
            {startError}
          </div>
        ) : !session ? (
          <div className="rounded-lg border border-surface-800 bg-surface-800/40 p-3 text-xs text-surface-400">
            Starting the sign-in flow on the remote machine…
          </div>
        ) : session.status === "completed" ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-700 dark:text-emerald-200">
            <div className="font-semibold mb-1">✓ Signed in</div>
            <div className="text-xs text-emerald-700 dark:text-emerald-300/80">{session.detail || "Auth stored on the remote machine."}</div>
          </div>
        ) : session.status === "failed" || session.status === "cancelled" ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-300">
            <div className="font-semibold mb-1">{session.status === "cancelled" ? "Cancelled" : "Failed"}</div>
            <div>{session.error || session.detail || "The CLI exited before sign-in completed."}</div>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-surface-400">
              Complete sign-in from any browser — we triggered <code className="rounded bg-surface-800 px-1.5 py-0.5 font-mono text-surface-200">{runner === "codex" ? "codex login --device-auth" : "claude auth login --claudeai"}</code> on the remote machine.
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

            {/* Claude flow: user signs in on platform.claude.com, copies
                the long auth token, and pastes it here. We forward it
                straight to the spawned CLI's stdin and never persist
                it (host-only, never to Convex). Codex's device-auth
                flow auto-completes — no paste step. */}
            {runner === "claude" && session.openUrl && (
              <div className="space-y-2 rounded-lg border border-indigo-500/30 bg-indigo-500/5 p-3">
                <div className="text-[10px] font-semibold uppercase tracking-widest text-indigo-700 dark:text-indigo-300">
                  Paste the auth code from platform.claude.com
                </div>
                <input
                  type="text"
                  value={authCode}
                  onChange={(e) => { setAuthCode(e.target.value); setSubmitError(null); }}
                  placeholder="EfaWvHCZ1pZWDZ3KZReKSnGdZDIpCn4viSCY4QLzSZ4bUYHV#…"
                  spellCheck={false}
                  autoComplete="off"
                  autoCorrect="off"
                  autoCapitalize="off"
                  className="w-full rounded-md border border-surface-700 bg-surface-950 px-3 py-2 font-mono text-[11px] text-surface-100 placeholder-surface-600 outline-none focus:border-indigo-400/60"
                  onPaste={(e) => {
                    // Tokens often have trailing whitespace from the
                    // copy button — trim aggressively so the user
                    // doesn't have to.
                    const pasted = e.clipboardData.getData("text") || "";
                    const cleaned = pasted.trim();
                    if (cleaned !== pasted) {
                      e.preventDefault();
                      setAuthCode(cleaned);
                    }
                  }}
                />
                <div className="flex items-center justify-between gap-2">
                  <p className="flex-1 text-[10px] text-surface-500 leading-relaxed">
                    Stays on this machine. Never goes to Convex.
                  </p>
                  <button
                    type="button"
                    disabled={submitting || authCode.trim().length < 8}
                    onClick={async () => {
                      const code = authCode.trim();
                      if (!code) return;
                      setSubmitting(true);
                      setSubmitError(null);
                      try {
                        const next = await clientRef.current!.submitRunnerBrowserAuthCode(session.id, code);
                        setSession(next);
                        // Clear the input immediately — we want zero
                        // window-of-exposure inside the React state
                        // tree once it's been forwarded to the agent.
                        setAuthCode("");
                      } catch (err) {
                        setSubmitError(err instanceof Error ? err.message : String(err));
                      } finally {
                        setSubmitting(false);
                      }
                    }}
                    className="shrink-0 rounded-md border border-indigo-400/40 bg-indigo-500/15 px-3 py-1 text-[11px] font-medium text-indigo-800 dark:text-indigo-100 hover:bg-indigo-500/25 disabled:opacity-50"
                  >
                    {submitting ? "Submitting…" : "Submit code"}
                  </button>
                </div>
                {submitError ? (
                  <p className="text-[10px] text-red-700 dark:text-red-300">{submitError}</p>
                ) : null}
              </div>
            )}

            <p className="text-[10px] text-surface-600 leading-relaxed">
              Auth codes are a common phishing target. Never share this code. Once the remote CLI confirms sign-in, this dialog turns green automatically.
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

class DeviceDetailsBoundary extends React.Component<{ device: Device; children: React.ReactNode }, { err: Error | null }> {
  state = { err: null as Error | null };
  static getDerivedStateFromError(err: Error) { return { err }; }
  componentDidCatch(err: Error) {
    if (typeof window !== "undefined" && (window as any).console) {
      console.error("[DeviceDetailsPanel crash]", this.props.device.id, err);
    }
  }
  render() {
    if (this.state.err) {
      return (
        <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-200">
          <div className="font-semibold">Details panel crashed</div>
          <div className="mt-1 text-[11px] text-red-700 dark:text-red-300/80">
            Likely an agent → dashboard schema mismatch (agent v{this.props.device.agentVersion || "?"} vs dashboard 1.1.32+).
            Toggling Details closed this panel; the rest of the dashboard is fine. Browser console has the stack trace.
          </div>
          <div className="mt-2 font-mono text-[10px] text-red-700 dark:text-red-300/60 break-all">{String(this.state.err.message || this.state.err)}</div>
        </div>
      );
    }
    return this.props.children;
  }
}

// RescueInlinePanel — the four rescue commands as buttons, plus the
// last queue status. Inline (not a modal) so the user stays anchored
// to the device card while picking. The panel posts to the
// Convex-backed rescue queue (web/lib/agent-client.ts queueRescueCommand);
// the agent picks the command up on its next heartbeat (~30 s) so
// this works even when the device's relay tunnel is wedged.
function RescueInlinePanel({
  device,
  statusMsg,
  onQueue,
  onReauth,
  onClose,
}: {
  device: Device;
  statusMsg?: { msg: string; tone: "info" | "ok" | "err" };
  onQueue: (command: "restart" | "reinstall-latest" | "tunnel-reset" | "auth-reset") => void;
  // Reset Auth uses the live /auth/recover path (direct mode → agent
  // rotates its bearer in place using ours). Distinct from the
  // Convex-backed onQueue path because the destructive auth-reset
  // command is rarely what the user actually wants — the headless
  // re-auth fixes 99 % of "not signed in on the box" cases without
  // requiring a physical re-pair.
  onReauth: () => void;
  onClose: () => void;
}) {
  const tone = statusMsg?.tone || "info";
  const toneCls =
    tone === "ok"
      ? "text-emerald-700 dark:text-emerald-300"
      : tone === "err"
        ? "text-red-700 dark:text-red-300"
        : "text-amber-800 dark:text-amber-200";
  return (
    <div className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/5">
      <div className="mb-2 flex items-center justify-between">
        <p className="text-[10px] font-semibold uppercase tracking-widest text-amber-800 dark:text-amber-300">
          Rescue {device.name}
        </p>
        <button
          onClick={onClose}
          className="text-[10px] text-amber-700/70 hover:text-amber-900 dark:text-amber-300/60 dark:hover:text-amber-200"
          title="Close"
        >
          close
        </button>
      </div>
      <p className="mb-3 text-[11px] text-amber-800/80 dark:text-amber-200/70">
        These commands ride on Convex (not the relay), so they work
        even when the agent&apos;s tunnel is broken. The agent picks
        the command up on its next heartbeat (~30 s).
      </p>
      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => onQueue("restart")}
          className="rounded border border-emerald-400 bg-emerald-50 px-2.5 py-1 text-[11px] text-emerald-800 hover:bg-emerald-100 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-200 dark:hover:bg-emerald-500/20"
          title="systemctl restart yaver-agent (Linux) — clears stale tunnels, picks up new config"
        >
          ↻ Restart
        </button>
        <button
          onClick={() => onQueue("reinstall-latest")}
          className="rounded border border-sky-400 bg-sky-50 px-2.5 py-1 text-[11px] text-sky-800 hover:bg-sky-100 dark:border-sky-500/40 dark:bg-sky-500/10 dark:text-sky-200 dark:hover:bg-sky-500/20"
          title="Download latest .deb from GitHub releases + dpkg -i + restart (Linux only)"
        >
          ⬇ Reinstall latest
        </button>
        <button
          onClick={() => onQueue("tunnel-reset")}
          className="rounded border border-indigo-400 bg-indigo-50 px-2.5 py-1 text-[11px] text-indigo-800 hover:bg-indigo-100 dark:border-indigo-500/40 dark:bg-indigo-500/10 dark:text-indigo-200 dark:hover:bg-indigo-500/20"
          title="Drop the relay tunnel and reconnect — same effect as restart today; lighter once the relay client gets a public Reset hook"
        >
          ⟳ Reset tunnel
        </button>
        <button
          onClick={onReauth}
          className="rounded border border-amber-400 bg-amber-50 px-2.5 py-1 text-[11px] text-amber-800 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/20"
          title="Send your web session bearer to the remote agent (POST /auth/recover mode=direct). Agent verifies ownership against Convex and rotates its token in place — no SSH, no re-pair."
        >
          ⟳ Reset Auth (headless re-auth)
        </button>
      </div>
      {statusMsg ? (
        <p className={`mt-3 break-all text-[11px] ${toneCls}`}>{statusMsg.msg}</p>
      ) : null}
    </div>
  );
}
