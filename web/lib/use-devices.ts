"use client";

import { useEffect, useState, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

/** DeviceStorage is the live disk gauge the agent sends on every heartbeat. */
export interface DeviceStorage {
  totalGb?: number;
  usedGb?: number;
  freeGb?: number;
  usedPct?: number;
  /** Aggregate reclaimable bytes, present only when the box has a warm scan.
   *  It's what turns "92% full" from alarming into actionable. */
  reclaimableGb?: number;
  updatedAt?: number;
}

export interface Device {
  id: string;
  name: string;
  /**
   * Per-user short alias. Set via `yaver alias set ...` on the CLI or
   * the inline editor in the dashboard. Lower-cased on the server,
   * unique within a single user's set of devices, used by
   * `yaver ssh <alias>` and as the display label whenever it's set.
   */
  alias?: string;
  platform: string;
  host: string;
  port: number;
  lastSeen: string;
  online: boolean;
  publicKey?: string;
  hardwareId?: string;
  hardwareProfile?: {
    os?: string;
    osVersion?: string;
    cpu?: string;
    gpu?: string;
    ramMb?: number;
    vramMb?: number;
    numCores?: number;
    arch?: string;
    /** True when the agent is running inside Windows Subsystem for
     *  Linux (detected via WSL_DISTRO_NAME / /proc/version on the
     *  host). Replaces the old IP-shape heuristic that false-
     *  positived on Linux boxes with Docker bridges. */
    isWsl?: boolean;
    /** Total capacity of the volume holding $HOME. A static spec like RAM, so
     *  it rides the 24h-gated hardware profile; live free/used is in `storage`. */
    diskTotalGb?: number;
    iosSimulators?: string[];
    androidEmulators?: string[];
  };
  /** Live disk gauge, refreshed on every heartbeat. Numbers only — the agent
   *  knows which project's caches are fat, but paths and project names stay on
   *  the device (they'd leak the home-dir username into Convex). */
  storage?: DeviceStorage;
  localIps?: string[];
  deviceClass?: "desktop" | "edge-mobile" | "server";
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
    preferredTasks: string[];
    memoryMb?: number;
    batteryPct?: number;
    isCharging?: boolean;
    thermalState?: "nominal" | "warm" | "hot";
  };
  isGuest?: boolean;
  /**
   * True when the agent's session token is revoked or expired. The agent
   * itself flips this on the device row via /devices/bootstrap when its
   * heartbeat 401s, so the dashboard can surface a "needs re-auth" UI
   * without the user having to attempt a connect first.
   */
  needsAuth?: boolean;
  hostName?: string;
  hostEmail?: string;
  /** Host's public userId string — identifies the share to POST /guests/leave. */
  hostUserIdString?: string;
  accessScope?: "owner" | "shared-scoped" | "shared-legacy";
  tunnelUrl?: string;
  publicEndpoints?: string[];
  priorityMode?: string;
  useHostApiKeys?: boolean;
  allowGuestProvidedApiKeys?: boolean;
  sharedWithGuests?: boolean;
  sharedGuests?: Array<{
    name?: string;
    email?: string;
  }>;
  sharesAllProjects?: boolean;
  sharedProjects?: string[];
  sharesAllRunners?: boolean;
  sharedRunners?: string[];
  runners?: Array<{
    runnerId?: string;
    status?: string;
  }>;
  installedRunnerIds?: string[];
  sessionBinding?: "dedicated" | "legacy-shared";
  lastTunnelEvent?: {
    online: boolean;
    at: number;
    peerAddr?: string;
    connectedAt?: number;
    durationSec?: number;
  };
  peerState?: "online" | "stale" | "offline";
  peerLastSeen?: string;
  workspaceLive?: boolean;
  probeState?: "ok" | "auth-expired" | "unreachable";
  probePath?: "relay" | "tunnel" | "direct";
  probeCheckedAt?: string;
  probeError?: string;
  probeInfo?: {
    hostname?: string;
    version?: string;
    platform?: string;
    workDir?: string;
    mode?: string;
    autoStart?: string;
    authExpired?: boolean;
    lifecycleState?: "bootstrap" | "yaver-auth-expired" | "ready-to-connect";
    lifecycle?: {
      state?: "bootstrap" | "yaver-auth-expired" | "ready-to-connect";
      usable?: boolean;
      recoverable?: boolean;
      recoveryMode?: string;
      supportsOwnerClaim?: boolean;
      ownerClaimReady?: boolean;
      requiresFirstPair?: boolean;
    };
    runtime?: Record<string, unknown>;
    system?: Record<string, unknown>;
  } | null;
  /**
   * Go-agent binary version reported by the device on register + heartbeat.
   * Absent when the device has never reported (older agent, or never registered
   * since the field was introduced). Surfaces as "no version info" in the UI.
   */
  agentVersion?: string;
  /** Epoch ms of the last server-side write of agentVersion. */
  agentVersionReportedAt?: number;
  /**
   * Hosting provenance from listMyDevices. "yaver-hosted" = a Yaver-managed box
   * (paid via LemonSqueezy or owner-adopted); "byo" = Yaver-provisioned on the
   * user's own cloud account; "self-hosted" = the user's own box.
   */
  hosting?: "yaver-hosted" | "byo" | "self-hosted";
  managed?: boolean;
  /** Managed-cloud machine id + status — needed to Pause (snapshot+delete) or
   *  Resume a Yaver-hosted box from the web dashboard, same as mobile does. */
  machineId?: string;
  machineStatus?: string;
  /** Backend verdict (isMachineWakeable): can this box actually be woken from a snapshot? */
  machineWakeable?: boolean;
  /**
   * True when this row lacks both hardwareId and publicKey AND is not a
   * guest. Such rows have unstable identity — a rename or platform
   * change splits them, and two unrelated boxes can collapse onto the
   * same (platform, name) key. We never use them as a reconnect target;
   * the UI surfaces a "re-pair from the box" warning instead.
   */
  ghost?: boolean;
}

interface DevicesState {
  devices: Device[];
  refreshDevices: () => Promise<void>;
}

// Mirrors backend/convex/devices.ts and mobile/_core/constants.ts.
// Agent beats every 5 min; 6 min = one missed beat + jitter buffer.
const HEARTBEAT_STALE_MS = 900_000;
let relayPresenceUrlPromise: Promise<string | null> | null = null;

async function getPrimaryRelayPresenceUrl(): Promise<string | null> {
  if (!relayPresenceUrlPromise) {
    relayPresenceUrlPromise = (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/config`);
        if (!res.ok) return null;
        const data = await res.json().catch(() => ({}));
        const relays = Array.isArray(data?.relayServers) ? data.relayServers : [];
        const primary = relays
          .filter((relay: any) => typeof relay?.httpUrl === "string" && relay.httpUrl.trim() !== "")
          .sort((a: any, b: any) => Number(a?.priority ?? 9999) - Number(b?.priority ?? 9999))[0];
        return primary?.httpUrl ? String(primary.httpUrl).replace(/\/+$/, "") : null;
      } catch {
        return null;
      }
    })();
  }
  return relayPresenceUrlPromise;
}

async function applyRelayPresence(devices: Device[]): Promise<Device[]> {
  if (devices.length === 0) return devices;
  const relayUrl = await getPrimaryRelayPresenceUrl();
  if (!relayUrl) return devices;
  try {
    const ids = devices.map((device) => device.id).filter(Boolean).join(",");
    if (!ids) return devices;
    const res = await fetch(`${relayUrl}/presence?ids=${encodeURIComponent(ids)}`);
    if (!res.ok) return devices;
    const data = await res.json().catch(() => ({}));
    const table = data?.devices && typeof data.devices === "object" ? data.devices : {};
    return devices.map((device) => {
      const entry = table[device.id];
      if (entry?.online === true) {
        // Bus-overrides-Convex: if the relay's presence endpoint
        // reports the agent has a live tunnel right now, we know
        // the host's session token is valid (the agent can't keep
        // its QUIC tunnel registered without one). Convex's
        // needsAuth flag goes stale between 5-minute heartbeats and
        // can flicker yellow even on a perfectly healthy agent —
        // this is the false-positive flicker users keep reporting.
        // Trust the live signal over the cached row.
        return {
          ...device,
          online: true,
          needsAuth: false,
          lastTunnelEvent: {
            online: true,
            at: Date.now(),
            connectedAt: typeof entry.connectedAt === "number" ? entry.connectedAt : undefined,
            durationSec: typeof entry.uptimeSec === "number" ? entry.uptimeSec : undefined,
          },
        };
      }
      return device;
    });
  } catch {
    return devices;
  }
}

function normalizedName(name: string | undefined): string {
  return String(name || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function normalizedHost(host: string | undefined): string {
  return String(host || "").trim().toLowerCase().replace(/\.local$/i, "");
}

function deviceIdentityKey(device: Device): string {
  if (device.isGuest) {
    const scope = device.hostEmail || device.hostName || "guest";
    return `guest:${scope}:${device.id || device.name}`;
  }
  // Stable cryptographic identity wins. hardwareId is the most stable
  // (survives renames and reinstalls); publicKey survives renames but
  // rotates on factory reset.
  if (device.hardwareId) return `hwid:${device.hardwareId}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
  // No stable identity. The previous fallback to `host:platform:name`
  // was a footgun: it merged unrelated boxes that happened to share a
  // hostname (common in fleets) and split a single box across renames.
  // Keep the row addressable per-deviceId — the `ghost` flag in
  // refreshDevices marks it so the UI can warn the user.
  if (device.id) return `id:${device.id}`;
  return `name:${device.name}`;
}

function deviceAliasKey(device: Device): string | null {
  if (device.isGuest) return null;
  const name = normalizedName(device.name);
  const platform = String(device.platform || "").trim().toLowerCase();
  if (!name || !platform) return null;
  return `${platform}:${name}`;
}

function deviceEndpointKey(device: Device): string | null {
  if (device.isGuest) return null;
  const host = normalizedHost(device.host);
  if (!host) return null;
  return `${host}:${device.port || 0}`;
}

function mergeIpLists(a?: string[], b?: string[]): string[] | undefined {
  const merged = new Set<string>();
  for (const ip of a || []) if (ip) merged.add(ip);
  for (const ip of b || []) if (ip) merged.add(ip);
  return merged.size > 0 ? [...merged] : undefined;
}

function mergeDeviceEntries(existing: Device, incoming: Device): Device {
  const incomingWins =
    (!existing.online && incoming.online) ||
    (Date.parse(incoming.lastSeen || "") || 0) > (Date.parse(existing.lastSeen || "") || 0);
  const base = incomingWins ? incoming : existing;
  const other = incomingWins ? existing : incoming;
  return {
    ...other,
    ...base,
    host: base.host || other.host,
    port: base.port || other.port,
    online: base.online || other.online,
    publicKey: base.publicKey || other.publicKey,
    hardwareId: base.hardwareId || other.hardwareId,
    hardwareProfile: base.hardwareProfile || other.hardwareProfile,
    lastTunnelEvent: (() => {
      const baseAt = base.lastTunnelEvent?.at || 0;
      const otherAt = other.lastTunnelEvent?.at || 0;
      if (baseAt === 0) return other.lastTunnelEvent;
      if (otherAt === 0) return base.lastTunnelEvent;
      return baseAt >= otherAt ? base.lastTunnelEvent : other.lastTunnelEvent;
    })(),
    localIps: mergeIpLists(base.localIps, other.localIps),
    publicEndpoints: (() => {
      const merged = new Set<string>();
      for (const endpoint of base.publicEndpoints || []) if (endpoint) merged.add(endpoint);
      for (const endpoint of other.publicEndpoints || []) if (endpoint) merged.add(endpoint);
      return merged.size > 0 ? [...merged] : undefined;
    })(),
    sharedGuests: (() => {
      const merged = new Map<string, { name?: string; email?: string }>();
      for (const guest of base.sharedGuests || []) {
        if (!guest?.name && !guest?.email) continue;
        merged.set(`${guest.email || ""}:${guest.name || ""}`, guest);
      }
      for (const guest of other.sharedGuests || []) {
        if (!guest?.name && !guest?.email) continue;
        merged.set(`${guest.email || ""}:${guest.name || ""}`, guest);
      }
      return merged.size > 0 ? [...merged.values()] : undefined;
    })(),
    runners:
      Array.isArray(base.runners) && base.runners.length > 0
        ? base.runners
        : other.runners,
    installedRunnerIds:
      Array.isArray(base.installedRunnerIds) && base.installedRunnerIds.length > 0
        ? base.installedRunnerIds
        : other.installedRunnerIds,
    lastSeen: (() => {
      const next = Math.max(Date.parse(existing.lastSeen || "") || 0, Date.parse(incoming.lastSeen || "") || 0);
      return next > 0 ? new Date(next).toISOString() : base.lastSeen || other.lastSeen;
    })(),
  };
}

function pickActiveOverStale(existing: Device, incoming: Device): Device | null {
  const existingDead = !existing.online;
  const incomingDead = !incoming.online;
  const existingLive = existing.online;
  const incomingLive = incoming.online;
  if (existingDead && incomingLive) return incoming;
  if (incomingDead && existingLive) return existing;
  return null;
}

function collapseDevices(devices: Device[]): Device[] {
  const byIdentity = new Map<string, Device>();
  for (const device of devices) {
    const key = deviceIdentityKey(device);
    const prev = byIdentity.get(key);
    byIdentity.set(key, prev ? mergeDeviceEntries(prev, device) : device);
  }

  const byAlias = new Map<string, Device>();
  for (const device of byIdentity.values()) {
    const key = deviceAliasKey(device);
    if (!key) {
      byAlias.set(`id:${device.id}`, device);
      continue;
    }
    const prev = byAlias.get(key);
    if (!prev) {
      byAlias.set(key, device);
      continue;
    }
    const strongConflict =
      (!!prev.hardwareId && !!device.hardwareId && prev.hardwareId !== device.hardwareId) ||
      (!!prev.publicKey && !!device.publicKey && prev.publicKey !== device.publicKey);
    if (strongConflict) {
      const winner = pickActiveOverStale(prev, device);
      if (winner) {
        byAlias.set(key, winner);
        continue;
      }
    }
    byAlias.set(key, mergeDeviceEntries(prev, device));
  }

  const byEndpoint = new Map<string, Device>();
  for (const device of byAlias.values()) {
    const key = deviceEndpointKey(device);
    if (!key) {
      byEndpoint.set(`id:${device.id}`, device);
      continue;
    }
    const prev = byEndpoint.get(key);
    byEndpoint.set(key, prev ? mergeDeviceEntries(prev, device) : device);
  }

  return [...byEndpoint.values()];
}

const HIDDEN_KEY = "yaver_hidden_device_ids";

function readHiddenIds(): Set<string> {
  if (typeof window === "undefined") return new Set();
  try {
    const raw = window.localStorage.getItem(HIDDEN_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    return new Set(Array.isArray(arr) ? arr.filter((x: any) => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function writeHiddenIds(ids: Set<string>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(HIDDEN_KEY, JSON.stringify([...ids]));
  } catch {
    // quota / private-mode — just drop
  }
}

export function hideDevice(id: string): void {
  const ids = readHiddenIds();
  ids.add(id);
  writeHiddenIds(ids);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("yaver-hidden-devices-changed"));
  }
}

export function unhideDevice(id: string): void {
  const ids = readHiddenIds();
  ids.delete(id);
  writeHiddenIds(ids);
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("yaver-hidden-devices-changed"));
  }
}

/**
 * Set or clear the per-user alias for a device. Pass alias="" (or
 * undefined) to clear. Server enforces per-user uniqueness — callers
 * surface the returned error verbatim ("alias already used …",
 * "alias invalid …") so the user knows what to fix.
 */
export async function setDeviceAlias(
  token: string,
  deviceId: string,
  alias: string,
): Promise<{ ok: true; alias: string | null } | { ok: false; error: string }> {
  try {
    const res = await fetch(`${CONVEX_URL}/devices/alias`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ deviceId, alias }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      return { ok: false, error: body?.error || `HTTP ${res.status}` };
    }
    return { ok: true, alias: body?.alias ?? null };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
}

export function unhideAll(): void {
  writeHiddenIds(new Set());
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent("yaver-hidden-devices-changed"));
  }
}

export function useDevices(token: string | null): DevicesState & { hiddenIds: Set<string> } {
  const [devices, setDevices] = useState<Device[]>([]);
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(() => readHiddenIds());

  // Re-read hidden set whenever hide/unhide fires (same tab) or the user hit
  // storage in another tab.
  useEffect(() => {
    const onChange = () => setHiddenIds(readHiddenIds());
    window.addEventListener("yaver-hidden-devices-changed", onChange);
    window.addEventListener("storage", onChange);
    return () => {
      window.removeEventListener("yaver-hidden-devices-changed", onChange);
      window.removeEventListener("storage", onChange);
    };
  }, []);

  const refreshDevices = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/devices/list`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) return;
      const raw = await res.json();
      const arr = Array.isArray(raw) ? raw : (raw.devices ?? []);

      // Map API fields to Device interface
      const mapped: Device[] = arr.map((d: any) => {
        const deviceId = d.deviceId || d.id || "";
        const rawHeartbeat = d.lastHeartbeat || d.lastSeen || 0;
        const heartbeatMs =
          typeof rawHeartbeat === "number"
            ? rawHeartbeat
            : rawHeartbeat
              ? Date.parse(String(rawHeartbeat))
              : 0;
        const online =
          (() => {
            const heartbeatFresh =
              Boolean(d.isOnline ?? d.online ?? false) &&
              heartbeatMs > 0 &&
              Date.now() - heartbeatMs < HEARTBEAT_STALE_MS;
            const tunnelEvent = d.lastTunnelEvent;
            const relayLive =
              tunnelEvent &&
              tunnelEvent.online === true &&
              typeof tunnelEvent.at === "number" &&
              Date.now() - tunnelEvent.at < HEARTBEAT_STALE_MS;
            return heartbeatFresh || relayLive;
          })();
        return {
        id: deviceId,
        name: d.isGuest ? `${d.name || d.hostname || ""} (${d.hostName || "guest"})` : d.name || d.hostname || "",
        alias: typeof d.alias === "string" && d.alias.trim() !== "" ? d.alias : undefined,
        platform: d.platform || "",
        host: d.quicHost || d.host || "",
        port: d.quicPort || d.port || 18080,
        lastSeen: heartbeatMs > 0 ? new Date(heartbeatMs).toISOString() : "",
        online,
        publicKey: d.publicKey,
        hardwareId: d.hardwareId ?? d.hwid,
        hardwareProfile: d.hardwareProfile ?? undefined,
        localIps: Array.isArray(d.localIps) ? d.localIps : undefined,
        deviceClass: d.deviceClass,
        edgeProfile: d.edgeProfile,
        isGuest: d.isGuest ?? false,
        needsAuth: Boolean(d.needsAuth ?? false),
        hostName: d.hostName,
        hostEmail: d.hostEmail,
        hostUserIdString: d.hostUserIdString,
        accessScope: d.accessScope,
        tunnelUrl: d.tunnelUrl,
        publicEndpoints: Array.isArray(d.publicEndpoints) ? d.publicEndpoints : undefined,
        priorityMode: d.priorityMode,
        useHostApiKeys: d.useHostApiKeys,
        allowGuestProvidedApiKeys: d.allowGuestProvidedApiKeys,
        sharedWithGuests: d.sharedWithGuests,
        sharedGuests: Array.isArray(d.sharedGuests) ? d.sharedGuests : undefined,
        sharesAllProjects: d.sharesAllProjects,
        sharedProjects: Array.isArray(d.sharedProjects) ? d.sharedProjects : undefined,
        sharesAllRunners: d.sharesAllRunners,
        sharedRunners: Array.isArray(d.sharedRunners) ? d.sharedRunners : undefined,
        runners: Array.isArray(d.runners) ? d.runners : undefined,
        installedRunnerIds: Array.isArray(d.installedRunnerIds) ? d.installedRunnerIds : undefined,
        sessionBinding: d.sessionBinding,
        lastTunnelEvent:
          d.lastTunnelEvent && typeof d.lastTunnelEvent === "object"
            ? {
                online: Boolean(d.lastTunnelEvent.online),
                at: typeof d.lastTunnelEvent.at === "number" ? d.lastTunnelEvent.at : 0,
                peerAddr: typeof d.lastTunnelEvent.peerAddr === "string" ? d.lastTunnelEvent.peerAddr : undefined,
                connectedAt: typeof d.lastTunnelEvent.connectedAt === "number" ? d.lastTunnelEvent.connectedAt : undefined,
                durationSec: typeof d.lastTunnelEvent.durationSec === "number" ? d.lastTunnelEvent.durationSec : undefined,
              }
            : undefined,
        agentVersion: typeof d.agentVersion === "string" ? d.agentVersion : undefined,
        agentVersionReportedAt:
          typeof d.agentVersionReportedAt === "number" ? d.agentVersionReportedAt : undefined,
        hosting:
          d.hosting === "yaver-hosted" || d.hosting === "byo" || d.hosting === "self-hosted"
            ? d.hosting
            : undefined,
        managed: typeof d.managed === "boolean" ? d.managed : undefined,
        machineId: typeof d.machineId === "string" ? d.machineId : undefined,
        machineStatus: typeof d.machineStatus === "string" ? d.machineStatus : undefined,
        machineWakeable: d.machineWakeable === true,
        // Ghost: non-guest row lacking both stable identifiers. Cannot
        // be reliably reconnect-targeted. Surfaced in the UI so the
        // user knows to re-pair from the device.
        ghost: !(d.hardwareId || d.hwid) && !d.publicKey && !d.isGuest,
      }});

      const collapsed = collapseDevices(mapped);
      const withRelayPresence = await applyRelayPresence(collapsed);

      // Stable order: preserve the previous ordering whenever the set
      // of device IDs hasn't changed. Devices only re-sort when one
      // appears or disappears — not every 10s poll, which would shuffle
      // the sidebar under the user's cursor as lastSeen timestamps tick.
      setDevices((prev) => {
        const prevIds = prev.map((d) => d.id);
        const nextIds = withRelayPresence.map((d) => d.id);
        const nextSet = new Set(nextIds);
        const sameMembership =
          prevIds.length === nextIds.length &&
          prevIds.every((id) => nextSet.has(id));

        if (sameMembership && prevIds.length > 0) {
          // Membership unchanged → keep the existing order, just merge
          // the fresh fields (online, lastSeen, peerState, …).
          const byId = new Map(withRelayPresence.map((d) => [d.id, d]));
          return prevIds
            .map((id) => byId.get(id))
            .filter((d): d is typeof withRelayPresence[number] => Boolean(d));
        }

        // Membership changed → sort once, freezing the new order until
        // the next add/remove. Pre-existing devices keep their place;
        // new devices land at the top of their online/offline bucket.
        const indexBefore = new Map(prevIds.map((id, i) => [id, i] as const));
        return [...withRelayPresence].sort((a, b) => {
          if (a.online !== b.online) return a.online ? -1 : 1;
          const ai = indexBefore.has(a.id) ? indexBefore.get(a.id)! : -1;
          const bi = indexBefore.has(b.id) ? indexBefore.get(b.id)! : -1;
          if (ai !== -1 && bi !== -1) return ai - bi;
          if (ai !== -1) return -1;
          if (bi !== -1) return 1;
          return b.lastSeen.localeCompare(a.lastSeen);
        });
      });
    } catch {
      // Silently fail
    }
  }, [token]);

  // Poll every 30s while the tab is visible, paused when backgrounded. Was a
  // flat 10s interval that ran even in a forgotten background tab — the single
  // biggest client-side Convex driver. Live peer presence still comes from the
  // relay in near-real-time; this poll only refreshes the durable device list.
  useVisiblePolling(refreshDevices, 30000);

  // Filter out hidden devices on the consumer side. We keep them in the raw
  // fetch so the "X hidden — show all" toggle can restore them instantly
  // without waiting for the next poll.
  const visible = devices.filter((d) => !hiddenIds.has(d.id));

  return { devices: visible, refreshDevices, hiddenIds };
}

// useVisiblePolling calls `fn` every `intervalMs` while the tab is visible,
// pauses entirely when the tab is hidden (a backgrounded dashboard shouldn't
// keep polling Convex forever), and does one immediate refresh on regaining
// visibility so the UI is fresh the moment you switch back. Passing a changed
// intervalMs resets the cadence — callers use that for adaptive polling.
function useVisiblePolling(fn: () => void, intervalMs: number) {
  useEffect(() => {
    let iv: ReturnType<typeof setInterval> | null = null;
    const stop = () => {
      if (iv) {
        clearInterval(iv);
        iv = null;
      }
    };
    const start = () => {
      if (!iv) iv = setInterval(fn, intervalMs);
    };
    const hidden = () =>
      typeof document !== "undefined" && document.hidden;
    const onVisibility = () => {
      if (hidden()) {
        stop();
      } else {
        fn(); // immediate refresh on return to foreground
        start();
      }
    };
    fn(); // initial fetch
    if (!hidden()) start();
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", onVisibility);
    }
    return () => {
      stop();
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onVisibility);
      }
    };
  }, [fn, intervalMs]);
}

// PendingDeviceClaim mirrors the row shape returned by
// /devices/pending-list. A pending claim is a bootstrap-mode box that
// joined the user's relay but has no Convex devices row yet — the
// dashboard surfaces it with a "Claim" CTA so a freshly-installed
// remote box becomes attached in one tap.
export interface PendingDeviceClaim {
  id: string;
  deviceId: string;
  hardwareId: string;
  name?: string;
  platform?: string;
  quicHost?: string;
  quicPort?: number;
  firstSeenAt: number;
  lastSeenAt: number;
  relayLabel?: string;
}

export function usePendingClaims(token: string | null): {
  pending: PendingDeviceClaim[];
  refreshPending: () => Promise<void>;
  claimPending: (deviceId: string, name?: string) => Promise<{ ok: boolean; error?: string }>;
} {
  const [pending, setPending] = useState<PendingDeviceClaim[]>([]);

  const refreshPending = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/devices/pending-list`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        // Silent — endpoint not deployed yet on older backends shouldn't
        // wedge the dashboard.
        return;
      }
      const data = await res.json().catch(() => ({}));
      const items: PendingDeviceClaim[] = Array.isArray(data?.items) ? data.items : [];
      setPending(items);
    } catch {
      // Network blip — keep prior list, retry next tick.
    }
  }, [token]);

  const claimPending = useCallback(
    async (deviceId: string, name?: string): Promise<{ ok: boolean; error?: string }> => {
      if (!token) return { ok: false, error: "Not signed in" };
      try {
        const res = await fetch(`${CONVEX_URL}/devices/pending-claim`, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ deviceId, name }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          return { ok: false, error: body?.error || `HTTP ${res.status}` };
        }
        // Refresh both lists. The pending row was deleted server-side
        // and a real devices row was created; the next /devices/list
        // poll will pick it up.
        await refreshPending();
        return { ok: true };
      } catch (e) {
        return { ok: false, error: e instanceof Error ? e.message : String(e) };
      }
    },
    [token, refreshPending],
  );

  // Adaptive + visibility-aware. A pending row means a bootstrap box is
  // waiting to be claimed, so poll fast (10s) to keep the Claim CTA snappy.
  // When there's nothing pending — the overwhelmingly common case — drop to
  // 60s; a freshly-installed remote box then appears within a minute, which
  // is plenty. Mirrors the agent-side guest-list idle backoff.
  const hasPending = pending.length > 0;
  useVisiblePolling(refreshPending, hasPending ? 10000 : 60000);

  return { pending, refreshPending, claimPending };
}
