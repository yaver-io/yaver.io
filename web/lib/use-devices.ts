"use client";

import { useEffect, useState, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

export interface Device {
  id: string;
  name: string;
  platform: string;
  host: string;
  port: number;
  lastSeen: string;
  online: boolean;
  publicKey?: string;
  hardwareId?: string;
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
}

interface DevicesState {
  devices: Device[];
  refreshDevices: () => Promise<void>;
}

// Mirrors backend/convex/devices.ts and mobile/_core/constants.ts.
// Agent beats every 5 min; 6 min = one missed beat + jitter buffer.
const HEARTBEAT_STALE_MS = 360_000;
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
        return {
          ...device,
          online: true,
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
  if (device.hardwareId) return `hwid:${device.hardwareId}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
  if (device.isGuest) {
    const scope = device.hostEmail || device.hostName || "guest";
    return `guest:${scope}:${device.id || device.name}`;
  }
  const name = normalizedName(device.name);
  const platform = String(device.platform || "").trim().toLowerCase();
  if (name && platform) return `host:${platform}:${name}`;
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
        platform: d.platform || "",
        host: d.quicHost || d.host || "",
        port: d.quicPort || d.port || 18080,
        lastSeen: heartbeatMs > 0 ? new Date(heartbeatMs).toISOString() : "",
        online,
        publicKey: d.publicKey,
        hardwareId: d.hardwareId ?? d.hwid,
        localIps: Array.isArray(d.localIps) ? d.localIps : undefined,
        deviceClass: d.deviceClass,
        edgeProfile: d.edgeProfile,
        isGuest: d.isGuest ?? false,
        needsAuth: Boolean(d.needsAuth ?? false),
        hostName: d.hostName,
        hostEmail: d.hostEmail,
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
      }});

      const collapsed = collapseDevices(mapped);
      const withRelayPresence = await applyRelayPresence(collapsed);

      withRelayPresence.sort((a, b) => {
        if (a.online !== b.online) return a.online ? -1 : 1;
        return b.lastSeen.localeCompare(a.lastSeen);
      });

      setDevices(withRelayPresence);
    } catch {
      // Silently fail
    }
  }, [token]);

  useEffect(() => {
    refreshDevices();
    // Poll every 10s
    const iv = setInterval(refreshDevices, 10000);
    return () => clearInterval(iv);
  }, [refreshDevices]);

  // Filter out hidden devices on the consumer side. We keep them in the raw
  // fetch so the "X hidden — show all" toggle can restore them instantly
  // without waiting for the next poll.
  const visible = devices.filter((d) => !hiddenIds.has(d.id));

  return { devices: visible, refreshDevices, hiddenIds };
}
