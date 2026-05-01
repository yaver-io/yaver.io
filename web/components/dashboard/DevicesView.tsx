"use client";

import Link from "next/link";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { type Device, hideDevice, unhideAll } from "@/lib/use-devices";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient, AgentClient, type AgentUpdateStatus, type RunnerBrowserAuthSession, type RunnerTestResult } from "@/lib/agent-client";
import { classifyTransport, fetchRelayHealth, type TransportInfo } from "@/lib/transport";

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
    case "needs-auth": return `${state.label}: installed but needs auth — set ANTHROPIC_API_KEY / OPENAI_API_KEY / etc. on the host`;
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
            className="ml-1 text-[10px] text-emerald-300"
            title={`Test passed in ${local.result.durationMs}ms${local.result.model ? ` (${local.result.model})` : ""}`}
          >
            ✓ {local.result.durationMs}ms
          </span>
        ) : null}
        {local.kind === "fail" ? (
          <span
            className="ml-1 text-[10px] text-red-300"
            title={local.result.error || "test failed"}
          >
            ✗ {local.result.probe || "failed"}
          </span>
        ) : null}
        {local.kind === "error" ? (
          <span className="ml-1 text-[10px] text-red-300" title={local.message}>
            ✗ unreachable
          </span>
        ) : null}
      </span>
      {canTest ? (
        <button
          onClick={runTest}
          disabled={local.kind === "running"}
          className="rounded-md border border-surface-700 bg-surface-950/60 px-1.5 py-0.5 text-[10px] font-semibold text-surface-300 hover:border-surface-600 hover:text-surface-100 disabled:opacity-60"
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
    // Choose the BEST base URL for this probe — preferring HTTPS
    // sources first so we don't trigger mixed-content blocks +
    // useless 502s on every device-row expand.
    //
    // Order:
    //   1. *.yaver.io publicEndpoint (relay-auto-provisioned
    //      <id>.dev.yaver.io) — HTTPS-direct, works for browsers
    //      behind NAT, no /d/<id>/ path noise.
    //   2. Any other publicEndpoint (Cloudflare tunnel, etc.).
    //   3. Active relay URL (current AgentClient session).
    //   4. Direct LAN IP — last resort, only worth trying when
    //      we're plausibly on the same network. From a different
    //      LAN this is the source of the 502 + mixed-content
    //      flood the user reported in devtools.
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
      // Browser is on http (rare — local dev). Only then is the
      // direct-LAN fetch even allowed; from https it's blocked
      // anyway, so don't bother adding it as a candidate.
      candidates.push(`http://${device.host}:${device.port}`);
    }
    if (candidates.length === 0) {
      setError("no reachable URL");
      setLoading(false);
      return;
    }
    (async () => {
      // Walk candidates in order until one answers /info successfully.
      // We use a short per-attempt timeout (2s) and only setError if
      // ALL candidates fail — otherwise the dashboard silently shows
      // nothing the moment any one URL is unreachable (e.g. wildcard
      // cert not yet provisioned, relay routing not yet wired, etc.).
      let lastErr = "no candidates";
      for (const base of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${base}/info`, {
            headers: { Authorization: `Bearer ${token}` },
            signal: AbortSignal.timeout(2_000),
          });
          if (!res.ok) {
            lastErr = `HTTP ${res.status}`;
            continue;
          }
          const data = await res.json();
          if (cancelled) return;
          setInfo(data);
          setError(null);
          // Opportunistic seed: if we can reach the device and it
          // reports a version string, push it to Convex so the
          // dashboard has it cached for other surfaces (mobile, other
          // browsers, the devices list for an offline-later view).
          // The mutation itself is 24h-gated so this is cheap to spam.
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
          // try next candidate
        }
      }
      if (!cancelled) {
        setError(lastErr);
        setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive, device.agentVersion, device.isGuest]);

  return { info, error, loading };
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
  const [loading, setLoading] = useState(false);
  const agentConnectionState = useAgentConnectionState();

  useEffect(() => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) return;
    let cancelled = false;
    setLoading(true);
    setError(null);

    // Same probe-ordering rules as useDeviceRuntimeInfo: prefer
    // HTTPS (relay path or *.yaver.io subdomain), only fall through
    // to direct LAN when the dashboard is on http (local dev). On
    // https://yaver.io, fetching http://<lan>/projects gets blocked
    // and we end up with a misleading "fetch failed" error.
    const candidates: string[] = [];
    if (agentClient.activeRelayUrl && device.id) {
      candidates.push(`${agentClient.activeRelayUrl}/d/${device.id}`);
    }
    const eps = (device.publicEndpoints || []).filter(Boolean).filter(isUsablePublicEndpoint);
    for (const ep of eps) {
      if (/^https:\/\//i.test(ep)) candidates.push(ep.replace(/\/+$/, ""));
    }
    if (typeof window !== "undefined" && window.location.protocol !== "https:") {
      candidates.push(`http://${device.host}:${device.port}`);
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
          setLoading(false);
        }
        return;
      }

      let lastErr = "no candidates";
      for (const base of candidates) {
        if (cancelled) return;
        try {
          const res = await fetch(`${base}/projects`, {
            headers: { Authorization: `Bearer ${token}` },
            signal: AbortSignal.timeout(3_000),
          });
          if (!res.ok) {
            lastErr = `HTTP ${res.status}`;
            continue;
          }
          const data = await res.json();
          const arr: any[] = Array.isArray(data) ? data : Array.isArray(data?.projects) ? data.projects : [];
          const mapped: DeviceProjectInfo[] = arr.map(mapAgentRow).filter((p: DeviceProjectInfo) => p.name.length > 0);
          if (cancelled) return;
          setProjects(mapped);
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
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive, agentConnectionState]);

  return { projects, error, loading };
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
  setPrimaryRunner: (deviceId: string, runnerId: string | null, model?: string | null) => Promise<void>;
} {
  const [runnerMap, setRunnerMap] = useState<Record<string, string>>({});
  const [modelMap, setModelMap] = useState<Record<string, string>>({});
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
          ? (data.settings.primaryRunnerByDevice as Array<{ deviceId: string; runnerId: string; model?: string }>)
          : [];
        if (!cancelled) {
          const runners: Record<string, string> = {};
          const models: Record<string, string> = {};
          for (const row of rows) {
            if (!row?.deviceId || !row?.runnerId) continue;
            runners[row.deviceId] = row.runnerId;
            if (row.model) models[row.deviceId] = row.model;
          }
          setRunnerMap(runners);
          setModelMap(models);
        }
      } catch {
        // best-effort — falls back to no per-device pref
      }
    })();
    return () => { cancelled = true; };
  }, [token, refreshNonce]);

  const setPrimaryRunner = useCallback(
    async (deviceId: string, runnerId: string | null, model?: string | null) => {
      if (!token) return;
      const previousRunner = runnerMap;
      const previousModel = modelMap;
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
      try {
        const body: Record<string, unknown> = {
          primaryRunnerForDevice: {
            deviceId,
            runnerId,
            ...(model !== undefined ? { model } : {}),
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
        throw e;
      }
    },
    [token, runnerMap, modelMap],
  );

  return { primaryRunnerByDevice: runnerMap, primaryModelByDevice: modelMap, setPrimaryRunner };
}

// Default model per runner when the user hasn't picked one yet.
// Applied when the user selects a primary runner and has no prior
// model choice, so `claude` seeds `opus-4-7` (user's explicit ask
// for "latest opus"), `codex` seeds `gpt-5-codex`, etc.
export const DEFAULT_MODEL_BY_RUNNER: Record<string, string> = {
  claude: "claude-opus-4-7",
  codex: "gpt-5.4",
};

export function isKivancAccount(email: string | null | undefined): boolean {
  return String(email || "").trim().toLowerCase() === "kivanc.cakmak@icloud.com";
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
      return "gpt-5.4";
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
    id: "glm",
    label: "GLM (Z.ai)",
    baseUrl: "https://api.z.ai/api/coding/paas/v4",
    requiresKey: true,
    keyEnv: "ZHIPU_API_KEY",
    blurb: "Zhipu coding plan — cheap GLM-4.6 with a large context window.",
    models: [
      { id: "glm-4.6", label: "GLM-4.6", hint: "coding-tuned, 128k ctx" },
      { id: "glm-4.5-air", label: "GLM-4.5 Air", hint: "lighter, faster" },
    ],
  },
  {
    id: "ollama",
    label: "Ollama (local, free)",
    baseUrl: "http://127.0.0.1:11434",
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
  const { primaryRunnerByDevice, primaryModelByDevice, setPrimaryRunner } = usePrimaryRunnerByDevice(token);
  // Latest released agent version from GitHub. Drives the per-device
  // "✓ latest" / "update available" badge + the remote-update button.
  const latestAgentVersion = useLatestAgentVersion();
  const [updateModalDevice, setUpdateModalDevice] = useState<Device | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [authModal, setAuthModal] = useState<{ device: Device; runner: string } | null>(null);
  // The "Rescue" inline panel — Convex-backed command queue that
  // works even when a device's relay tunnel is wedged (the agent's
  // heartbeat polls Convex on a separate path). Tracks which device's
  // panel is open + the latest queued command for status feedback.
  const [rescueOpenDeviceId, setRescueOpenDeviceId] = useState<string | null>(null);
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
            <div key={device.id} className="card flex items-start gap-4 border border-slate-200 bg-white shadow-sm dark:border-surface-700/80 dark:bg-[rgba(44,46,56,0.82)] dark:shadow-[0_18px_40px_rgba(0,0,0,0.22),inset_0_1px_0_rgba(255,255,255,0.03)]">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-slate-100 text-slate-500 dark:bg-[rgba(18,19,24,0.92)] dark:text-surface-300">
                <DeviceIcon platform={device.platform} />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="font-semibold text-slate-900 dark:text-surface-50">
                        {device.name}
                      </h3>
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
                              ? "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-300"
                              : "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300"
                          }`}
                        >
                          {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Shared Session"}
                        </span>
                      ) : null}
                      {(() => {
                        const lifecycle = deriveDeviceLifecycleState(device);
                        const dotClass =
                          lifecycle === "connected"
                            ? "bg-emerald-300"
                            : lifecycle === "bootstrap"
                              ? "bg-violet-400"
                              : lifecycle === "yaver-auth-expired"
                                ? "bg-amber-400"
                                : lifecycle === "ready-to-connect"
                                  ? "bg-cyan-400"
                                  : "bg-surface-600";
                        const label =
                          lifecycle === "connected"
                            ? "Connected"
                            : lifecycle === "bootstrap"
                              ? "Bootstrap"
                              : lifecycle === "yaver-auth-expired"
                                ? "Yaver Auth Expired"
                                : lifecycle === "ready-to-connect"
                                  ? "Ready to Connect"
                                  : "Offline";
                        return (
                          <>
                            <span className={`inline-flex h-2 w-2 rounded-full ${dotClass}`} />
                            <span className="text-xs text-slate-500 dark:text-surface-500">
                              {label}
                            </span>
                          </>
                        );
                      })()}
                    </div>
                    <div className="mt-1"><TransportBadge device={device} /></div>
                    <p className="text-sm text-slate-600 dark:text-surface-500">
                      {devicePlatformLabel(device)} · Last agent signal {formatLastSeen(device.lastSeen)}
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
                            return (
                              <button
                                onClick={() => setUpdateModalDevice(device)}
                                title={`Update v${cur} → v${latestAgentVersion} on ${device.name}`}
                                className="ml-2 rounded-full border border-amber-300 bg-amber-50 px-1.5 py-px text-[10px] font-semibold uppercase tracking-wider text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300 dark:hover:bg-amber-500/20"
                              >
                                update → v{latestAgentVersion}
                              </button>
                            );
                          })() : null}
                        </>
                      ) : null}
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
                            ? "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200"
                            : "border-slate-300 bg-white text-slate-700 hover:border-amber-300 hover:text-amber-700 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.82)] dark:text-surface-300 dark:hover:border-amber-500/30 dark:hover:text-amber-200"
                        }`}
                        title={primaryDeviceId === device.id ? "This is your primary device" : "Mark this device as your primary machine"}
                      >
                        <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                          <path d="m12 2.75 2.33 4.72 5.21.76-3.77 3.67.89 5.19L12 14.6l-4.66 2.49.89-5.19-3.77-3.67 5.21-.76L12 2.75Z" />
                        </svg>
                        {primaryDeviceId === device.id ? "Primary" : "Set as primary"}
                      </button>
                    ) : null}
                    {!device.isGuest ? (
                      <button
                        onClick={() => setRescueOpenDeviceId(rescueOpenDeviceId === device.id ? null : device.id)}
                        className="inline-flex items-center gap-1.5 rounded-md border border-amber-300 bg-amber-50 px-2.5 py-1 text-[11px] font-medium text-amber-700 hover:border-amber-400 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-300 dark:hover:border-amber-500/60 dark:hover:bg-amber-500/20"
                        title="Recover a wedged agent — works even when the relay tunnel is broken"
                      >
                        🩹 Rescue
                      </button>
                    ) : null}
                    <button
                      onClick={() => setExpandedId(expandedId === device.id ? null : device.id)}
                      className="inline-flex items-center gap-1.5 rounded-md border border-slate-300 bg-white px-2.5 py-1 text-[11px] font-medium text-slate-700 hover:border-slate-400 hover:bg-slate-50 dark:border-surface-700 dark:bg-[rgba(20,21,27,0.82)] dark:text-surface-300 dark:hover:border-surface-600 dark:hover:bg-[rgba(31,33,41,0.94)] dark:hover:text-surface-100"
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
                    onClose={() => setRescueOpenDeviceId(null)}
                  />
                ) : null}
                {device.edgeProfile ? (
                  <p className="text-xs text-slate-500 dark:text-surface-500">
                    {device.edgeProfile.supportsLocalInference ? "Local inference" : "No local inference"} · max {device.edgeProfile.maxModelClass} model · {device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}
                  </p>
                ) : null}
                {shareSummary?.viewerIsGuest && shareSummary?.hostLabel ? (
                  <div className="mt-3">
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-500">
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
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-500">
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
                  // Seed a sensible default from the device's actual
                  // runner health. Only seeds when no explicit pref
                  // exists yet — never overrides a user choice.
                  const explicitPrimary = primaryRunnerByDevice[device.id];
                  const seededPrimary = (() => {
                    if (explicitPrimary) return explicitPrimary;
                    const readyIds = states.filter((s) => s.health === "ready").map((s) => s.id);
                    return preferredDefaultRunnerForDevice(device, signedInEmail, readyIds);
                  })();
                  const primaryId = explicitPrimary ?? seededPrimary ?? "";
                  const primaryState = states.find((s) => s.id === primaryId);
                  // "Available" = anything that's actually present on
                  // the agent. We strip "not-installed" entries so the
                  // dropdown / chip rail isn't full of dead options the
                  // user can't act on. The whitelist used to also gate
                  // this rail to the three "vibing-grade" runners
                  // (claude/codex/opencode), which hid aider /
                  // aider-ollama / ollama from the user even when they
                  // were installed — confusing on a machine that
                  // genuinely has six runners ready. Now we surface
                  // every installed runner here; the whitelist still
                  // governs the *primary* picker so the default vibing
                  // experience doesn't accidentally land on a local
                  // model. Test / Sign-in business logic is unchanged
                  // — we just route them through RunnerChipWithTest.
                  const availableStates = states.filter((s) => s.health !== "not-installed");
                  const availableOthers = availableStates.filter((s) => s.id !== primaryId);
                  return (
                    <div className="mt-3">
                      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-500">
                        Coding agents
                      </div>
                      {/* Primary agent — promoted to its own card. This
                          is the default runner used when chat / hot
                          reload / web reload opens a workspace on this
                          device, so we make it visually load-bearing
                          instead of one chip among many. */}
                      <div className="mb-2 flex flex-wrap items-center gap-2 rounded-lg border border-indigo-300 bg-indigo-50 px-3 py-2 dark:border-indigo-500/30 dark:bg-indigo-500/5">
                        <span className="flex items-center gap-1 text-[10px] font-semibold uppercase tracking-widest text-indigo-700 dark:text-indigo-300">
                          <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                            <path d="M12 2l2.39 7.36H22l-6.18 4.49L18.21 22 12 17.51 5.79 22l2.39-8.15L2 9.36h7.61z"/>
                          </svg>
                          Primary
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
                            title="Suggested default based on which runners are ready on this device. Click Confirm to persist."
                          >
                            suggested
                          </span>
                        ) : null}
                        <span className="ml-auto flex items-center gap-1.5">
                          <select
                            value={primaryId}
                            onChange={(e) => {
                              const next = e.target.value || null;
                              // When switching to a runner that has model
                              // presets, seed the default so the user
                              // doesn't land on an empty "(default)" for
                              // a runner where it matters. Preserve any
                              // existing explicit model when the user is
                              // re-selecting the same runner.
                              const seeded = next ? preferredDefaultModelForRunner(next, device, signedInEmail) : undefined;
                              const curModel = primaryModelByDevice[device.id];
                              const prevRunner = primaryRunnerByDevice[device.id];
                              const model = next && prevRunner === next && curModel
                                ? curModel
                                : seeded ?? null;
                              void setPrimaryRunner(device.id, next, model).catch(() => {});
                            }}
                            className="rounded border border-indigo-300 bg-white px-2 py-1 text-[12px] font-medium text-indigo-700 hover:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400/40 dark:border-indigo-500/30 dark:bg-surface-900 dark:text-indigo-100 dark:hover:border-indigo-400/50"
                            title="Change primary coding agent for this device. Auto-selected in every Yaver surface (chat, hot reload, web reload, mobile) when this device is active."
                          >
                            <option value="">(none)</option>
                            {availableStates.map((s) => (
                              <option key={s.id} value={s.id}>
                                {s.label}{s.health === "needs-auth" ? " · signs-in" : ""}
                              </option>
                            ))}
                          </select>
                          {/* Model selector — only surfaces when the
                              current primary runner actually has model
                              presets (claude / codex). OpenCode picks
                              its model through its own provider+key
                              flow in the chat input, so it has no
                              preset list here. */}
                          {primaryId && MODEL_OPTIONS_BY_RUNNER[primaryId] ? (
                            <select
                              value={
                                primaryModelByDevice[device.id]
                                  ?? preferredDefaultModelForRunner(primaryId, device, signedInEmail)
                                  ?? ""
                              }
                              onChange={(e) => {
                                const nextModel = e.target.value || null;
                                void setPrimaryRunner(device.id, primaryId, nextModel).catch(() => {});
                              }}
                              className="rounded border border-indigo-300 bg-white px-2 py-1 text-[11px] text-indigo-700 hover:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400/40 dark:border-indigo-500/30 dark:bg-surface-900 dark:text-indigo-100 dark:hover:border-indigo-400/50"
                              title={`Model used when spawning ${primaryId}. Forwarded as --model / env var to the runner.`}
                            >
                              {MODEL_OPTIONS_BY_RUNNER[primaryId].map((m) => (
                                <option key={m.id} value={m.id} title={m.hint || ""}>
                                  {m.label}
                                </option>
                              ))}
                            </select>
                          ) : null}
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
                        </span>
                      </div>
                      {/* Other available agents — collapsed by default
                          since the user already chose a primary. Click
                          to expose the full chip rail with Test +
                          Sign-in buttons preserved for each one. */}
                      {availableOthers.length > 0 ? (
                        <details className="rounded-lg border border-slate-200 bg-slate-50/70 dark:border-surface-700/70 dark:bg-[rgba(22,24,31,0.78)]">
                          <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[11px] text-slate-600 hover:text-slate-900 dark:text-surface-400 dark:hover:text-surface-200">
                            <span>Other available agents</span>
                            <span className="text-[10px] text-slate-500 dark:text-surface-500">({availableOthers.length})</span>
                          </summary>
                          <div className="flex flex-wrap items-center gap-1.5 border-t border-slate-200 px-3 py-2 dark:border-surface-800/60">
                            {availableOthers.map((state) => (
                              <RunnerChipWithTest
                                key={`${device.id}:runner:${state.id}`}
                                device={device}
                                state={state}
                                token={token ?? null}
                                onSignIn={(runnerId) => setAuthModal({ device, runner: runnerId })}
                              />
                            ))}
                          </div>
                        </details>
                      ) : null}
                      {/* Projects on this machine — same fold-with-count
                          shape as "Other available agents" so the device
                          card stays scannable. Each chip surfaces the
                          stack badge, a git-configured marker, and a
                          monorepo marker. Click for the richer
                          per-project view inside the Details panel. */}
                      {!device.isGuest ? (
                        <DeviceProjectsRail device={device} token={token ?? null} onShowDetails={() => setExpandedId(device.id)} />
                      ) : null}
                    </div>
                  );
                })()}
                <div className="mt-5 flex flex-wrap items-center gap-2">
                  <InlinePingButton device={device} token={token ?? null} />
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
                        device.online
                          ? "bg-indigo-600 text-white hover:bg-indigo-500 dark:bg-indigo-500 dark:hover:bg-indigo-400"
                          : "border border-amber-300 bg-amber-50 text-amber-700 hover:bg-amber-100 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/20"
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
// Limitation: today only updates the device the dashboard is
// connected to (agentClient.baseUrl is implicit). Updating a peer
// device would need a /peer/<id>/agent/update path — leave for a
// later iteration.
function AgentUpdateModal({
  device,
  latestVersion,
  token: _token,
  onClose,
}: {
  device: Device;
  latestVersion: string;
  token: string;
  onClose: () => void;
}) {
  const [phase, setPhase] = useState<string>("starting");
  const [lines, setLines] = useState<Array<{ phase: string; text: string }>>([]);
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmedVersion, setConfirmedVersion] = useState<string | null>(null);
  const [downloadBytes, setDownloadBytes] = useState<{ read: number; total: number } | null>(null);
  // Tick state so the user sees something move while we wait for
  // the first SSE event from the agent. Flips every 500ms; the
  // spinner / shimmer in the modal reads from this.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (done || error) return;
    const t = setInterval(() => setTick((n) => (n + 1) % 1_000_000), 500);
    return () => clearInterval(t);
  }, [done, error]);

  useEffect(() => {
    let cancelled = false;
    const abort = new AbortController();

    const pollForNewVersion = async () => {
      const deadline = Date.now() + 90_000; // 90s budget for the restart
      while (!cancelled && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 2500));
        try {
          const info = await agentClient.getInfo();
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
        // Kick off the update on the agent. Returns started=true
        // when an update is now in flight, started=false when the
        // agent thinks it's already on the latest version. 409
        // means an update was already running — totally fine,
        // we'll just attach to the existing stream.
        let started = true;
        try {
          const triggerResp = await agentClient.triggerAgentUpdate();
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

        const streamUrl = agentClient.agentUpdateStreamUrl;
        if (!streamUrl) {
          if (!cancelled) setError("Could not resolve agent stream URL — is the device connected?");
          return;
        }
        const streamRes = await fetch(streamUrl, {
          headers: agentClient.getAuthHeaders(),
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
            // Only kick the poll if no progress event has arrived.
            // Detected by phase still being the initial "starting".
            // Reading state from a closure is fragile — use a ref-
            // less heuristic: if we never moved past "starting"
            // for 45s, the agent likely doesn't emit progress.
            // pollForNewVersion is idempotent.
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
    };
  }, [device.id, latestVersion]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget && (done || error)) onClose(); }}
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
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
            <div className="mb-1 font-semibold">Update failed</div>
            <div>{error}</div>
          </div>
        ) : done ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-200">
            <div className="mb-1 font-semibold">Updated</div>
            <div className="text-xs text-emerald-300/80">
              {device.name} now reports v{confirmedVersion}.
            </div>
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
                  : "bg-indigo-400 animate-pulse";
              const subtitle = (() => {
                if (phase === "starting" || phase === "queued") {
                  // First few seconds — no agent event yet. Use the
                  // tick spinner so the user sees motion.
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
                  <div className="mb-2 h-2 w-full overflow-hidden rounded-full bg-surface-800">
                    <div
                      className={`h-full ${phase === "error" ? "bg-red-500" : "bg-indigo-500"} transition-all duration-300`}
                      style={{ width: `${Math.max(2, pct)}%` }}
                    />
                  </div>
                  {subtitle ? (
                    <p className="mb-2 text-[11px] text-surface-400">{subtitle}</p>
                  ) : null}
                  <pre className="max-h-48 overflow-auto rounded-lg border border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-400 whitespace-pre-wrap">
                    {lines.length === 0
                      ? `Connecting to ${device.name}…`
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
  const { projects, error, loading } = useDeviceProjects(device, !device.isGuest, token);

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
    <details className="rounded-lg border border-surface-800 bg-surface-900/30">
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[11px] text-surface-400 hover:text-surface-200">
        <span>Git projects</span>
        <span className="text-[10px] text-surface-500">{headerCount}</span>
      </summary>
      <div className="flex flex-wrap items-center gap-1.5 border-t border-surface-800/60 px-3 py-2">
        {loading ? (
          <span className="text-[10px] text-surface-500">Loading project list from agent…</span>
        ) : error ? (
          <span className="text-[10px] text-surface-600">
            Project list unavailable — agent transport returned: {error}.
          </span>
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
                className="inline-flex items-center gap-1 rounded border border-emerald-500/40 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-emerald-200 hover:bg-emerald-500/20"
                title={tip || undefined}
              >
                <span className="text-emerald-100">{p.name}</span>
                {stack ? (
                  <span className="rounded bg-emerald-500/15 px-1 text-[9px] font-normal normal-case text-emerald-300/80">
                    {stack}
                  </span>
                ) : null}
                {/* Git-configured marker. The little link glyph means
                    the project has a configured `origin` remote and is
                    pushable; absence means the dir is on disk but has
                    no git history yet. */}
                {hasGit ? (
                  <span className="text-emerald-300/80" title={`git remote: ${p.remote}`}>⌬</span>
                ) : (
                  <span className="text-surface-600" title="no git remote configured">∅</span>
                )}
                {/* Monorepo-app marker. Filled when the agent's
                    workspace manifest declares this project as one app
                    inside a multi-app yaver.workspace.yaml — distinct
                    from a top-level repo. */}
                {isMonorepoApp ? (
                  <span className="text-amber-300/80" title={`monorepo app · root ${p.monorepoRoot}`}>◫</span>
                ) : null}
              </button>
            );
          })
        )}
      </div>
    </details>
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
        className="rounded-md border border-rose-500/40 bg-rose-500/10 px-2.5 py-1 text-[11px] font-medium text-rose-200 hover:border-rose-400 hover:text-rose-100 disabled:opacity-50"
        title="Wipe the agent's local auth_token + device_id and put it back into bootstrap (pairing) mode. Use when the box has someone else's session and AUTH/recover can't fix it."
      >
        {busy ? "Resetting..." : "Reset Auth"}
      </button>
      {msg && (
        <span className={`text-[10px] ${msg.startsWith("✓") ? "text-emerald-300" : "text-rose-300"}`}>
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
  const { info, error, loading } = useDeviceRuntimeInfo(device, true, token);
  const { status: updateStatus, error: updateError, loading: updateLoading, updating, trigger: triggerUpdate } =
    useDeviceAgentUpdate(device, true, token);
  const effectiveInfo = (info || device.probeInfo || null) as DeviceRuntimeInfo | null;
  const { pingState, ping } = useDevicePing(device, token);
  // Guests list /projects under a host-shared-scope allowlist but we
  // never want to display raw owner workdir paths to a guest — cap it
  // to owner sessions for now.
  const { projects: liveProjects, error: projectsError, loading: projectsLoading } =
    useDeviceProjects(device, !device.isGuest, token);
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
      <div className="mb-3 flex flex-wrap justify-end gap-2">
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
            className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2.5 py-1 text-[11px] font-medium text-amber-200 hover:border-amber-400 hover:text-amber-100 disabled:opacity-50"
            title={`Update this machine from ${currentVersion || "current"} to ${latestVersion}. The agent may restart and disconnect briefly.`}
          >
            {updating || updateStatus?.updating ? "Updating..." : `Update to v${String(latestVersion).replace(/^v/i, "")}`}
          </button>
        ) : null}
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
        {!device.isGuest ? (
          <FactoryResetAuthButton device={device} />
        ) : null}
      </div>
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
                    ? "border-amber-500/40 bg-amber-500/10 text-amber-200"
                    : "border-emerald-500/40 bg-emerald-500/10 text-emerald-200"
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
                  className="inline-flex items-center gap-1 rounded border border-emerald-500/40 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-emerald-200"
                  title={[p.path, p.branch && `branch: ${p.branch}`, p.framework].filter(Boolean).join(" · ") || undefined}
                >
                  {p.name}
                  {p.framework ? (
                    <span className="text-[9px] font-normal normal-case text-emerald-300/70">
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
          ) : (
            <p className="text-[11px] text-surface-600">
              {projectsError
                ? `Project list unavailable — agent unreachable (${projectsError}).`
                : "Project list unavailable — agent offline."}
            </p>
          )}
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
      {updateError ? (
        <p className="mt-2 text-[11px] text-surface-600">
          Update status unavailable ({updateError}).
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
  // Claude's modern OAuth flow returns a long token the user must
  // paste back into the CLI on the remote machine. We pipe that paste
  // through the agent's /runner-auth/browser/submit-code endpoint
  // straight into the spawned `claude auth login --console` stdin.
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

            {/* Claude flow: user signs in on platform.claude.com, copies
                the long auth token, and pastes it here. We forward it
                straight to the spawned CLI's stdin and never persist
                it (host-only, never to Convex). Codex's device-auth
                flow auto-completes — no paste step. */}
            {runner === "claude" && session.openUrl && (
              <div className="space-y-2 rounded-lg border border-indigo-500/30 bg-indigo-500/5 p-3">
                <div className="text-[10px] font-semibold uppercase tracking-widest text-indigo-300">
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
                    className="shrink-0 rounded-md border border-indigo-400/40 bg-indigo-500/15 px-3 py-1 text-[11px] font-medium text-indigo-100 hover:bg-indigo-500/25 disabled:opacity-50"
                  >
                    {submitting ? "Submitting…" : "Submit code"}
                  </button>
                </div>
                {submitError ? (
                  <p className="text-[10px] text-red-300">{submitError}</p>
                ) : null}
              </div>
            )}

            <p className="text-[10px] text-surface-600 leading-relaxed">
              Device codes are a common phishing target. Never share this code. Once you finish in the browser, this dialog turns green automatically.
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
        <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-200">
          <div className="font-semibold">Details panel crashed</div>
          <div className="mt-1 text-[11px] text-red-300/80">
            Likely an agent → dashboard schema mismatch (agent v{this.props.device.agentVersion || "?"} vs dashboard 1.1.32+).
            Toggling Details closed this panel; the rest of the dashboard is fine. Browser console has the stack trace.
          </div>
          <div className="mt-2 font-mono text-[10px] text-red-300/60 break-all">{String(this.state.err.message || this.state.err)}</div>
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
  onClose,
}: {
  device: Device;
  statusMsg?: { msg: string; tone: "info" | "ok" | "err" };
  onQueue: (command: "restart" | "reinstall-latest" | "tunnel-reset" | "auth-reset") => void;
  onClose: () => void;
}) {
  const [confirmReset, setConfirmReset] = useState(false);
  const tone = statusMsg?.tone || "info";
  const toneCls =
    tone === "ok"
      ? "text-emerald-300"
      : tone === "err"
        ? "text-red-300"
        : "text-amber-200";
  return (
    <div className="mt-3 rounded-md border border-amber-500/30 bg-amber-500/5 p-3">
      <div className="mb-2 flex items-center justify-between">
        <p className="text-[10px] font-semibold uppercase tracking-widest text-amber-300">
          Rescue {device.name}
        </p>
        <button
          onClick={onClose}
          className="text-[10px] text-amber-300/60 hover:text-amber-200"
          title="Close"
        >
          close
        </button>
      </div>
      <p className="mb-3 text-[11px] text-amber-200/70">
        These commands ride on Convex (not the relay), so they work
        even when the agent&apos;s tunnel is broken. The agent picks
        the command up on its next heartbeat (~30 s).
      </p>
      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => onQueue("restart")}
          className="rounded border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-[11px] text-emerald-200 hover:bg-emerald-500/20"
          title="systemctl restart yaver-agent (Linux) — clears stale tunnels, picks up new config"
        >
          ↻ Restart
        </button>
        <button
          onClick={() => onQueue("reinstall-latest")}
          className="rounded border border-sky-500/40 bg-sky-500/10 px-2.5 py-1 text-[11px] text-sky-200 hover:bg-sky-500/20"
          title="Download latest .deb from GitHub releases + dpkg -i + restart (Linux only)"
        >
          ⬇ Reinstall latest
        </button>
        <button
          onClick={() => onQueue("tunnel-reset")}
          className="rounded border border-indigo-500/40 bg-indigo-500/10 px-2.5 py-1 text-[11px] text-indigo-200 hover:bg-indigo-500/20"
          title="Drop the relay tunnel and reconnect — same effect as restart today; lighter once the relay client gets a public Reset hook"
        >
          ⟳ Reset tunnel
        </button>
        <button
          onClick={() => {
            if (!confirmReset) {
              setConfirmReset(true);
              return;
            }
            setConfirmReset(false);
            onQueue("auth-reset");
          }}
          className={`rounded border px-2.5 py-1 text-[11px] ${
            confirmReset
              ? "border-red-500/60 bg-red-500/20 text-red-100 hover:bg-red-500/30"
              : "border-red-500/40 bg-red-500/10 text-red-200 hover:bg-red-500/20"
          }`}
          title="Move ~/.yaver/config.json aside — device becomes unauthenticated; requires re-pair on next boot"
        >
          {confirmReset ? "Confirm Reset Auth?" : "⚠ Reset Auth"}
        </button>
      </div>
      {statusMsg ? (
        <p className={`mt-3 break-all text-[11px] ${toneCls}`}>{statusMsg.msg}</p>
      ) : null}
    </div>
  );
}
