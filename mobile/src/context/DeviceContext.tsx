import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Alert, AppState, AppStateStatus, Linking, Platform } from "react-native";
import Constants from "expo-constants";
import NetInfo from "@react-native-community/netinfo";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { router } from "expo-router";
import { quicClient, RecoveryResult, RelayServer, TunnelServer } from "../lib/quic";
import { useAuth } from "./AuthContext";
import { getLocalSecret, getUserSettings, saveUserSettings, LOCAL_KEYS } from "../lib/auth";
import { appLog } from "../lib/logger";
import { beaconListener, type DiscoveredDevice } from "../lib/beacon";
import { fetchPairInfo, submitPair } from "../lib/pairDevice";
import { submitEncryptedPair } from "../lib/encryptedPair";
import { CONVEX_SITE_URL } from "../lib/constants";
import {
  fetchGuestHosts,
  acceptGuestInvitation as apiAcceptInvitation,
  acceptGuestByCode as apiAcceptByCode,
  inviteGuest as apiInviteGuest,
} from "../lib/guests";

/** User-scoped storage key. Falls back to global key if no userId. */
function userKey(userId: string | undefined, key: string): string {
  return userId ? `@yaver/u/${userId}/${key}` : `@yaver/${key}`;
}

// Exported so settings screen can read/write with user scope
export function customRelaysKey(userId?: string): string { return userKey(userId, "custom_relays"); }
export function customTunnelsKey(userId?: string): string { return userKey(userId, "custom_tunnels"); }
function relayOnboardingKey(userId?: string): string { return userKey(userId, "relay_onboarding_done"); }
function relaySyncKey(userId?: string): string { return userKey(userId, "relay_sync_enabled"); }
function debugLogsKey(): string { return "@yaver/debug_logs_enabled"; } // global, not per-user

// Build the tunnel-server list passed to quicClient.connect for a given
// device. Merges two sources: (a) `device.tunnelUrl` — the host-wide
// single tunnel URL from their userSettings, used when a host shares
// only one machine; (b) `device.publicEndpoints` — the agent-advertised
// Cloudflare tunnel URLs from /devices/heartbeat publicEndpoints,
// per-device and authoritative. Deduplicated, stable order, host-wide
// tunnel last so per-device endpoints race first.
function tunnelServersForDevice(device: Pick<Device, "id" | "name" | "tunnelUrl" | "publicEndpoints">): TunnelServer[] | undefined {
  const seen = new Set<string>();
  const out: TunnelServer[] = [];
  const add = (url: string, priority: number, label: string) => {
    const trimmed = url.trim().replace(/\/+$/, "");
    if (!trimmed || seen.has(trimmed)) return;
    seen.add(trimmed);
    out.push({ id: `tunnel-${device.id}-${out.length}`, url: trimmed, label, priority });
  };
  (device.publicEndpoints ?? []).forEach((u, i) => add(u, i, `${device.name} endpoint #${i + 1}`));
  if (device.tunnelUrl) add(device.tunnelUrl, out.length, `${device.name} shared tunnel`);
  return out.length > 0 ? out : undefined;
}

// Legacy keys for migration
export const CUSTOM_RELAYS_KEY = "@yaver/custom_relays";
export const CUSTOM_TUNNELS_KEY = "@yaver/custom_tunnels";
const RELAY_ONBOARDING_KEY = "@yaver/relay_onboarding_done";

const DETACHED_DEVICES_KEY = "@yaver/detached_devices";

async function getDetachedDevices(): Promise<Set<string>> {
  try {
    const raw = await AsyncStorage.getItem(DETACHED_DEVICES_KEY);
    return raw ? new Set(JSON.parse(raw)) : new Set();
  } catch { return new Set(); }
}

/**
 * Ask the primary relay server which of these devices have an active QUIC
 * tunnel right now. The relay is authoritative for tunnel state; heartbeat
 * lags by up to ~90 s. When the relay reports a device online we flip
 * online=true on its record immediately, which makes the auto-connect rule
 * and the device list react to real state instead of the last heartbeat.
 *
 * Best-effort: any failure (no relays configured, relay down, network error)
 * returns the input list unchanged.
 */
async function applyRelayPresence(list: Device[]): Promise<Device[]> {
  if (list.length === 0) return list;
  const relays = quicClient.relayServersSnapshot;
  if (!relays || relays.length === 0) return list;
  const relay = relays[0]; // highest priority
  try {
    const ids = list.map((d) => d.id).filter(Boolean).join(",");
    const url = `${relay.httpUrl}/presence?ids=${encodeURIComponent(ids)}`;
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), 3000);
    const res = await fetch(url, { signal: ctrl.signal });
    clearTimeout(timer);
    if (!res.ok) return list;
    const data = await res.json();
    const table = (data && data.devices) || {};
    return list.map((d) => {
      const entry = table[d.id];
      if (entry && entry.online === true) {
        return { ...d, online: true, lastSeen: Math.max(d.lastSeen || 0, Date.now()) };
      }
      return d;
    });
  } catch {
    return list;
  }
}

async function addDetachedDevice(key: string): Promise<void> {
  const detached = await getDetachedDevices();
  detached.add(key);
  await AsyncStorage.setItem(DETACHED_DEVICES_KEY, JSON.stringify([...detached]));
}

let _debugLogsEnabled = false;
// Load debug preference on module init
AsyncStorage.getItem("@yaver/debug_logs_enabled").then((val) => {
  _debugLogsEnabled = val === "true";
});

const APP_VERSION = Constants.expoConfig?.version ?? "unknown";
const BUILD_NUMBER =
  Constants.expoConfig?.ios?.buildNumber ??
  Constants.expoConfig?.android?.versionCode?.toString() ??
  "unknown";

// Heartbeat freshness window. Re-exported from @yaver/client-core (the
// mirror at src/_core/constants.ts) so mobile, Feedback SDK, and the
// backend all agree on the same number. Drift here previously produced
// "green on one, yellow on the other" UX glitches from clock skew
// alone. See ARCHITECTURE_CLIENT_CORE.md.
import { HEARTBEAT_STALE_MS } from "../_core/constants";

export interface RunnerInfo {
  taskId: string;
  runnerId: string;
  model?: string;
  pid: number;
  status: string;
  title: string;
}

export interface Device {
  id: string;
  name: string;
  host: string;
  port: number;
  online: boolean;
  lastSeen: number;
  os: string;
  runners: RunnerInfo[];
  /** X25519 public key (base64) for encrypted pairing — stored in Convex */
  publicKey?: string;
  /** true when the agent is running in bootstrap mode (no valid token) */
  needsAuth?: boolean;
  /** true when device is discovered via LAN beacon (same network) */
  local?: boolean;
  /** stable hardware ID (P2P only, never sent to Convex) */
  hwid?: string;
  /** Real-time tunnel event from the relay (optional — only populated
   * when the relay has CONVEX_PRESENCE_URL + _SECRET wired). Mobile
   * shows a "relay-online since X" badge when this is fresher than
   * the last heartbeat, because it reflects tunnel state with ~2s
   * latency vs the 30s heartbeat window. */
  lastTunnelEvent?: {
    online: boolean;
    at: number;           // epoch ms
    peerAddr?: string;
    connectedAt?: number; // epoch ms
    durationSec?: number; // set on disconnect
  };
  /** every reachable IPv4 the agent broadcast in heartbeat — Wi-Fi LAN,
   * Tailscale 100.x, Ethernet, etc. The connect path races them in
   * parallel so the session attaches via whichever has a route from
   * the phone right now (e.g. Tailscale on cellular, Wi-Fi on same LAN).
   */
  lanIps?: string[];
  /** true when this device belongs to a host who granted us guest access */
  isGuest?: boolean;
  /** host's display name (only set when isGuest=true) */
  hostName?: string;
  /** host's email (only set when isGuest=true) */
  hostEmail?: string;
  /** owner vs explicitly shared vs legacy broad sharing */
  accessScope?: "owner" | "shared-scoped" | "shared-legacy";
  /** host scheduling / priority hint for shared usage */
  priorityMode?: string;
  /** host-advertised tunnel hint for this one shared device */
  tunnelUrl?: string;
  /** agent-advertised Cloudflare / public tunnel URLs (from /devices/heartbeat publicEndpoints). Used as a connect fallback between direct LAN and relay. */
  publicEndpoints?: string[];
  /** guest may use host-managed credentials without seeing raw secret */
  useHostApiKeys?: boolean;
  /** guest may bring their own credentials on top of host infra */
  allowGuestProvidedApiKeys?: boolean;
  /** device is shared to one or more guests */
  sharedWithGuests?: boolean;
  /** share grants are not project-scoped */
  sharesAllProjects?: boolean;
  /** explicitly shared project scopes when narrowed */
  sharedProjects?: string[];
  /** share grants are not runner-scoped */
  sharesAllRunners?: boolean;
  /** explicitly shared runners when narrowed */
  sharedRunners?: string[];
  /** whether this owned device is on its own dedicated backend session */
  sessionBinding?: "dedicated" | "legacy-shared";
  /** broad role of the node in Yaver scheduling */
  deviceClass?: "desktop" | "edge-mobile" | "server";
  /** edge/mobile capability hints for placement */
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
    preferredTasks: Array<"speech" | "ocr" | "vision" | "embedding" | "rerank" | "automation" | "small-llm">;
    memoryMb?: number;
    batteryPct?: number;
    isCharging?: boolean;
    thermalState?: "nominal" | "warm" | "hot";
  };
}

function normalizedDeviceName(name: string | undefined): string {
  return String(name || "")
    .trim()
    .toLowerCase()
    .replace(/\.local$/i, "");
}

function deviceAliasKey(device: Pick<Device, "name" | "os" | "isGuest">): string | null {
  if (device.isGuest) return null;
  const normalizedName = normalizedDeviceName(device.name);
  const normalizedOs = String(device.os || "").trim().toLowerCase();
  if (!normalizedName || !normalizedOs) return null;
  return `${normalizedOs}:${normalizedName}`;
}

function normalizedHost(host: string | undefined): string {
  return String(host || "")
    .trim()
    .toLowerCase()
    .replace(/\.local$/i, "");
}

function normalizedURL(url: string | undefined): string {
  return String(url || "")
    .trim()
    .replace(/\/+$/, "")
    .toLowerCase();
}

function dedupeRelayServers(servers: RelayServer[]): RelayServer[] {
  const seen = new Set<string>();
  const out: RelayServer[] = [];
  for (const server of servers) {
    const key = normalizedURL(server.httpUrl) || `${server.id}:${server.quicAddr}`;
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(server);
  }
  return out.sort((a, b) => a.priority - b.priority);
}

function resolveRelayServers(
  platformServers: RelayServer[],
  accountRelayUrl?: string,
  accountRelayPassword?: string,
): RelayServer[] {
  const platform = dedupeRelayServers(platformServers || []);
  const relayUrl = normalizedURL(accountRelayUrl);
  if (!relayUrl) return platform;

  const matched = platform.filter((server) => normalizedURL(server.httpUrl) === relayUrl);
  if (matched.length > 0) {
    return dedupeRelayServers([
      ...matched.map((server) => ({
        ...server,
        password: accountRelayPassword || server.password,
        priority: 1,
      })),
      ...platform.map((server) =>
        normalizedURL(server.httpUrl) === relayUrl
          ? {
              ...server,
              password: accountRelayPassword || server.password,
              priority: 1,
            }
          : server
      ),
    ]);
  }

  return dedupeRelayServers([
    {
      id: "account",
      quicAddr: "",
      httpUrl: accountRelayUrl!.trim(),
      region: "account",
      priority: 1,
      password: accountRelayPassword,
    },
    ...platform,
  ]);
}

function deviceEndpointKey(device: Pick<Device, "host" | "port" | "isGuest">): string | null {
  if (device.isGuest) return null;
  const host = normalizedHost(device.host);
  if (!host) return null;
  return `${host}:${device.port || 0}`;
}

function deviceIdentityKey(device: Pick<Device, "id" | "hwid" | "publicKey" | "name" | "os" | "isGuest" | "hostEmail" | "hostName">): string {
  if (device.hwid) return `hwid:${device.hwid}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
  if (device.isGuest) {
    const hostScope = device.hostEmail || device.hostName || "guest";
    return `guest:${hostScope}:${device.id || device.name}`;
  }
  const normalizedName = normalizedDeviceName(device.name);
  if (normalizedName && device.os) {
    return `host:${device.os}:${normalizedName}`;
  }
  if (device.id) return `id:${device.id}`;
  return `name:${device.name}`;
}

function mergeDeviceEntries(existing: Device, incoming: Device): Device {
  const incomingWins =
    (!!existing.needsAuth && !incoming.needsAuth) ||
    incoming.lastSeen > existing.lastSeen ||
    (!!incoming.online && !existing.online) ||
    (!!incoming.local && !existing.local);

  if (incomingWins) {
    return {
      ...existing,
      ...incoming,
      runners: incoming.runners?.length ? incoming.runners : existing.runners,
      publicKey: incoming.publicKey || existing.publicKey,
      hwid: incoming.hwid || existing.hwid,
      host: incoming.host || existing.host,
      port: incoming.port || existing.port,
      lastSeen: Math.max(existing.lastSeen || 0, incoming.lastSeen || 0),
    };
  }

  return {
    ...incoming,
    ...existing,
    host: existing.host || incoming.host,
    port: existing.port || incoming.port,
    online: existing.online || incoming.online,
    local: existing.local || incoming.local,
    runners: existing.runners?.length ? existing.runners : incoming.runners,
    publicKey: existing.publicKey || incoming.publicKey,
    hwid: existing.hwid || incoming.hwid,
    lastSeen: Math.max(existing.lastSeen || 0, incoming.lastSeen || 0),
  };
}

// pickActiveOverStaleNeedsAuth returns whichever of the two device
// records should "win" when they share an alias key (os + hostname)
// but have differing hwid/publicKey. The strong signal "this is a
// leftover registration, not a second physical machine" is
// `needsAuth + offline` paired with `authenticated + online` on the
// other side — that pattern only happens when the agent re-paired
// (or was wiped + reinstalled) and the previous Convex row never
// got cleaned up. Time-since-last-seen turned out to be a bad
// secondary check because Convex back-dates `lastHeartbeat` from
// the first sync, so a 1-hour-old leftover still showed up as a
// duplicate. Hide it without the staleness gate.
function pickActiveOverStaleNeedsAuth(a: Device, b: Device): Device | null {
  const aDead = !!a.needsAuth && !a.online;
  const bDead = !!b.needsAuth && !b.online;
  const aLive = !a.needsAuth && a.online;
  const bLive = !b.needsAuth && b.online;
  if (aDead && bLive) return b;
  if (bDead && aLive) return a;
  return null;
}

function collapseAliasDevices(devices: Device[]): Device[] {
  const byIdentity = new Map<string, Device>();
  for (const device of devices) {
    const key = deviceIdentityKey(device);
    const existing = byIdentity.get(key);
    byIdentity.set(key, existing ? mergeDeviceEntries(existing, device) : device);
  }

  const byAlias = new Map<string, Device>();
  for (const device of byIdentity.values()) {
    const alias = deviceAliasKey(device);
    if (!alias) {
      byAlias.set(`id:${device.id}`, device);
      continue;
    }
    const existing = byAlias.get(alias);
    if (!existing) {
      byAlias.set(alias, device);
      continue;
    }
    // Same hostname + OS = same physical machine as far as the user
    // is concerned, even if hwid/publicKey differ. That split
    // happens naturally when the agent re-pairs (new config), when
    // the same machine registers once via LAN and once via a VPN
    // IP, or when a stale Convex row lingers after a wipe.
    // Collapsing these into one card stops the picker showing
    // "Kvancs-MacBook-Air.local" twice with different IPs.
    //
    // Two genuinely separate machines sharing a hostname is rare
    // enough (users almost never rename their Mac) that we accept
    // the edge case in exchange for a clean list. If it ever
    // matters we can surface it via the strong-identity path
    // behind a user flag.
    const hasStrongIdentity =
      (!!existing.hwid && !!device.hwid && existing.hwid !== device.hwid) ||
      (!!existing.publicKey && !!device.publicKey && existing.publicKey !== device.publicKey);
    if (hasStrongIdentity) {
      // Prefer the authenticated + online record when the other
      // side is a stale "needs auth" leftover.
      const winner = pickActiveOverStaleNeedsAuth(existing, device);
      if (winner) {
        byAlias.set(alias, winner);
        continue;
      }
    }
    byAlias.set(alias, mergeDeviceEntries(existing, device));
  }

  const byEndpoint = new Map<string, Device>();
  for (const device of byAlias.values()) {
    const endpoint = deviceEndpointKey(device);
    if (!endpoint) {
      byEndpoint.set(`id:${device.id}`, device);
      continue;
    }
    const existing = byEndpoint.get(endpoint);
    byEndpoint.set(endpoint, existing ? mergeDeviceEntries(existing, device) : device);
  }

  return [...byEndpoint.values()];
}

type ConnectionStatus = "disconnected" | "connecting" | "connected" | "error";

interface GuestInvitation {
  /** Convex row id — present on records fetched from the backend. */
  _id?: string;
  inviteId?: string;
  inviteCode?: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  hostUserIdString?: string;
  createdAt: number;
  expiresAt: number;
  invitedByUserId?: boolean;
  proposedDeviceIds?: string[];
}

interface DeviceState {
  devices: Device[];
  activeDevice: Device | null;
  connectionStatus: ConnectionStatus;
  isLoadingDevices: boolean;
  /** true when user explicitly disconnected (not a network failure) */
  userDisconnected: boolean;
  /** Last connection error message (null if no error) */
  lastError: string | null;
  /** true when agent's Convex auth session is expired (agent reachable but needs re-auth) */
  agentAuthExpired: boolean;
  /** Trigger phone-driven auth recovery for a device. */
  recoverDeviceAuth: (device: Device) => Promise<RecoveryResult | null>;
  selectDevice: (device: Device) => Promise<void>;
  disconnect: () => void;
  refreshDevices: () => Promise<void>;
  detachDevice: (device: Device) => Promise<void>;
  removeDevice: (device: Device) => Promise<void>;
  /** Device IDs the phone has failed to reach this session. Cleared on successful connect. */
  unreachableDeviceIds: string[];
  /** Flag a device as not reachable (e.g. after user hit Stop on a reconnect loop). */
  markDeviceUnreachable: (deviceId: string) => void;
  /** Devices where auto-pair has repeatedly failed; the user needs to run
   *  `yaver auth` on that machine manually. UI can surface a soft banner. */
  manualAuthRequiredDeviceIds: string[];
  /** Stop the active reconnect loop, clear the active device, mark it unreachable, and refresh from Convex. */
  stopReconnectAndBounce: () => Promise<void>;
  /** Pending guest invitations from other users */
  guestInvitations: GuestInvitation[];
  /** Accept a guest invitation by email match. Optional approvedDeviceIds narrows scope. */
  acceptGuestInvitation: (hostUserId: string, approvedDeviceIds?: string[]) => Promise<void>;
  /** Accept a guest invitation by 6-char invite code (works with any OAuth email). */
  acceptGuestByCode: (
    code: string,
    approvedDeviceIds?: string[],
  ) => Promise<{ hostName: string; hostEmail: string }>;
  /** Invite someone as a guest to your machine. Accepts email or userId. */
  inviteGuest: (
    target: string | { email?: string; userId?: string; deviceIds?: string[] },
  ) => Promise<{ inviteCode: string; guestRegistered: boolean; guestUserId?: string; guestEmail?: string }>;
  /** User's preferred device for auto-connect when multiple machines exist. */
  primaryDeviceId: string | null;
  /** Persist the preferred device. Pass null to clear. Syncs to Convex so
   *  other surfaces (web, desktop, MCP) honor the same choice. */
  setPrimaryDevice: (deviceId: string | null) => Promise<void>;
}

const DeviceContext = createContext<DeviceState | undefined>(undefined);

/** Fire-and-forget telemetry to Convex + in-app logger (best-effort, never throws). */
function sendTelemetry(token: string | null, step: string, message: string, details?: string) {
  const level = step.includes("fail") ? "error" : "info";
  appLog(level as "info" | "error", `[${step}] ${message}${details ? " | " + details : ""}`);
  if (!_debugLogsEnabled) return;
  fetch(`${CONVEX_SITE_URL}/mobile/log`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify({
      level, step, message,
      details: details?.slice(0, 2000),
      platform: Platform.OS,
      appVersion: APP_VERSION,
      buildNumber: BUILD_NUMBER,
    }),
  }).catch(() => {});
}

export function DeviceProvider({ children }: { children: React.ReactNode }) {
  const { token, user } = useAuth();
  const uid = user?.id;

  // User-scoped storage keys (different user = different settings)
  const RELAYS_KEY = customRelaysKey(uid);
  const TUNNELS_KEY = customTunnelsKey(uid);
  const ONBOARDING_KEY = relayOnboardingKey(uid);
  const SYNC_KEY = relaySyncKey(uid);

  // Migrate legacy global keys to user-scoped on first load
  const migrated = useRef(false);
  useEffect(() => {
    if (!uid || migrated.current) return;
    migrated.current = true;
    (async () => {
      // Migrate relays
      const scopedRelays = await AsyncStorage.getItem(RELAYS_KEY);
      if (!scopedRelays) {
        const legacy = await AsyncStorage.getItem(CUSTOM_RELAYS_KEY);
        if (legacy) await AsyncStorage.setItem(RELAYS_KEY, legacy);
      }
      // Migrate tunnels
      const scopedTunnels = await AsyncStorage.getItem(TUNNELS_KEY);
      if (!scopedTunnels) {
        const legacy = await AsyncStorage.getItem(CUSTOM_TUNNELS_KEY);
        if (legacy) await AsyncStorage.setItem(TUNNELS_KEY, legacy);
      }
    })().catch(() => {});
  }, [uid, RELAYS_KEY, TUNNELS_KEY]);

  const [devices, setDevices] = useState<Device[]>([]);
  const [activeDevice, setActiveDevice] = useState<Device | null>(null);
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("disconnected");
  const [isLoadingDevices, setIsLoadingDevices] = useState(false);
  const [userDisconnected, setUserDisconnected] = useState(false);
  const [relaysReady, setRelaysReady] = useState(false);
  const [lastError, setLastError] = useState<string | null>(null);
  const [guestInvitations, setGuestInvitations] = useState<GuestInvitation[]>([]);
  const [agentAuthExpired, setAgentAuthExpired] = useState(false);
  const [unreachableSet, setUnreachableSet] = useState<Set<string>>(() => new Set());
  // Auto-pair failure tracking. Each auto-pair path (LAN beacon / relay /
  // direct) records per-device failures; after MAX_PAIR_ATTEMPTS we add
  // the deviceId to `manualAuthRequiredSet` and the polling loops skip
  // it. Reset automatically the next time we observe a successful pair.
  const pairAttemptsRef = useRef<Map<string, number>>(new Map());
  const [manualAuthRequiredSet, setManualAuthRequiredSet] = useState<Set<string>>(() => new Set());
  // User-chosen "primary" machine — auto-connect target when they have more
  // than one device. Undefined = no preference set → force manual pick for
  // multi-device users. Loaded from Convex on mount, persisted on change.
  const [primaryDeviceId, setPrimaryDeviceIdState] = useState<string | null>(null);
  const hasLoadedOnce = useRef(false);

  const markDeviceUnreachable = useCallback((deviceId: string) => {
    setUnreachableSet((prev) => {
      if (prev.has(deviceId)) return prev;
      const next = new Set(prev);
      next.add(deviceId);
      return next;
    });
  }, []);

  const clearDeviceUnreachable = useCallback((deviceId: string) => {
    setUnreachableSet((prev) => {
      if (!prev.has(deviceId)) return prev;
      const next = new Set(prev);
      next.delete(deviceId);
      return next;
    });
  }, []);

  // Auto-pair bookkeeping — shared across the 3 auto-pair effects so
  // failures on one path count against the overall budget for that
  // device. 5 attempts spans LAN + relay + direct probes, which is
  // enough to rule out transient network trouble and signal to the
  // user that the machine genuinely needs manual `yaver auth`.
  const MAX_AUTO_PAIR_ATTEMPTS = 5;
  const recordAutoPairFailure = useCallback((deviceId: string) => {
    const next = (pairAttemptsRef.current.get(deviceId) ?? 0) + 1;
    pairAttemptsRef.current.set(deviceId, next);
    if (next >= MAX_AUTO_PAIR_ATTEMPTS) {
      setManualAuthRequiredSet((prev) => {
        if (prev.has(deviceId)) return prev;
        const updated = new Set(prev);
        updated.add(deviceId);
        appLog(
          "warn",
          `[auto-pair] Giving up on ${deviceId} after ${next} attempts — run 'yaver auth' on that machine`
        );
        return updated;
      });
    }
  }, []);
  const recordAutoPairSuccess = useCallback((deviceId: string) => {
    pairAttemptsRef.current.delete(deviceId);
    setManualAuthRequiredSet((prev) => {
      if (!prev.has(deviceId)) return prev;
      const updated = new Set(prev);
      updated.delete(deviceId);
      return updated;
    });
  }, []);
  const isAutoPairBlocked = useCallback((deviceId: string) => {
    return (pairAttemptsRef.current.get(deviceId) ?? 0) >= MAX_AUTO_PAIR_ATTEMPTS;
  }, []);

  const refreshDevices = useCallback(async () => {
    if (!token) {
      appLog("info", "refreshDevices: no token, skipping");
      return;
    }
    appLog("info", "refreshDevices: fetching...");
    // Only show loading spinner on initial load, not background refreshes
    if (!hasLoadedOnce.current) {
      setIsLoadingDevices(true);
    }
    try {
      // Only fetch device list — settings are loaded once on startup, not every poll
      const devicesRes = await fetch(`${CONVEX_SITE_URL}/devices/list`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      appLog("info", `/devices/list status: ${devicesRes.status}`);

      if (devicesRes.ok) {
        const data = await devicesRes.json();
        const raw = data.devices || data || [];
        appLog("info", `Found ${raw.length} device(s)`);
        const connectedDeviceId = quicClient.isConnected ? activeDevice?.id : null;
        const mapped: Device[] = raw.map((d: any) => {
          const deviceId = d.deviceId || d.id;
          // If we're actively connected to this device, trust our connection over stale heartbeat
          const isActivelyConnected = connectedDeviceId === deviceId;
          return {
            id: deviceId,
            name: d.isGuest ? `${d.name} (${d.hostName || "guest"})` : d.name,
            host: d.quicHost || d.host,
            port: d.quicPort || d.port,
            online: isActivelyConnected || (() => {
              const flag = d.isOnline ?? d.online ?? false;
              const lastSeen = d.lastHeartbeat || d.lastSeen || 0;
              return flag && lastSeen > 0 && (Date.now() - lastSeen) < HEARTBEAT_STALE_MS;
            })(),
            lastSeen: isActivelyConnected ? Date.now() : (d.lastHeartbeat || d.lastSeen || 0),
            os: d.platform || d.os || "",
            runners: d.runners ?? [],
            publicKey: d.publicKey,
            hwid: d.hardwareId || d.hwid,
            lanIps: Array.isArray(d.localIps) ? d.localIps : undefined,
            lastTunnelEvent: d.lastTunnelEvent ?? undefined,
            needsAuth: d.needsAuth ?? false,
            isGuest: d.isGuest || false,
            hostName: d.hostName,
            hostEmail: d.hostEmail,
            accessScope: d.accessScope,
            tunnelUrl: d.tunnelUrl,
            publicEndpoints: Array.isArray(d.publicEndpoints) ? d.publicEndpoints : undefined,
            priorityMode: d.priorityMode,
            useHostApiKeys: d.useHostApiKeys,
            allowGuestProvidedApiKeys: d.allowGuestProvidedApiKeys,
            sharedWithGuests: d.sharedWithGuests,
            sharesAllProjects: d.sharesAllProjects,
            sharedProjects: Array.isArray(d.sharedProjects) ? d.sharedProjects : undefined,
            sharesAllRunners: d.sharesAllRunners,
            sharedRunners: Array.isArray(d.sharedRunners) ? d.sharedRunners : undefined,
            sessionBinding: d.sessionBinding,
            deviceClass: d.deviceClass,
            edgeProfile: d.edgeProfile,
          };
        });
        // Deduplicate by stable device identity. Guest devices must include
        // host context so two different hosts with the same machine name
        // cannot collapse into one visible entry.
        const collapsed = collapseAliasDevices(mapped);
        // Filter out detached devices
        const detached = await getDetachedDevices();
        const filtered = collapsed.filter(d => {
          const key = deviceIdentityKey(d);
          return !detached.has(key);
        });
        // Real-time presence override: ask the primary relay server which
        // devices have an active QUIC tunnel RIGHT NOW. This signal is
        // authoritative — heartbeat can be up to ~90 s stale, but the relay
        // knows tunnel up/down the instant it happens. If the relay says
        // online, we flip online=true regardless of heartbeat freshness;
        // if it says offline we leave the heartbeat-based flag alone
        // (could still be LAN-only and not using the relay at all).
        // Best-effort: any failure leaves the list unchanged.
        const finalDevices = await applyRelayPresence(filtered);
        setDevices(finalDevices);
      } else {
        appLog("warn", `/devices/list failed: ${devicesRes.status}`);
      }

      // Fetch pending guest invitations
      try {
        const hosts = await fetchGuestHosts(token);
        setGuestInvitations(hosts.pending || []);
      } catch {
        // Non-critical — don't fail device refresh
      }
    } catch (e) {
      appLog("error", `refreshDevices error: ${e}`);
    } finally {
      hasLoadedOnce.current = true;
      setIsLoadingDevices(false);
    }
  }, [token]);

  const selectDevice = useCallback(
    async (device: Device) => {
      if (!token) return;

      // Clear user-disconnect flag when user (or auto-connect) selects a device
      setUserDisconnected(false);
      setLastError(null);

      if (quicClient.isConnected) {
        quicClient.disconnect();
      }

      setConnectionStatus("connecting");
      setActiveDevice(device);
      setAgentAuthExpired(false);

      try {
        sendTelemetry(token, "connect-start", `Connecting to ${device.name}`, JSON.stringify({
          host: device.host, port: device.port, deviceId: device.id.slice(0, 8),
          relayCount: quicClient.relayServerCount,
        }));
        // Race connect against a 10s timeout. Pass every reachable IP the
        // agent has reported in heartbeat (Wi-Fi LAN, Tailscale 100.x,
        // Ethernet) so quicClient can race them in parallel against the
        // beacon and Convex-stored primary host.
        const connectPromise = quicClient.connect(
          device.host,
          device.port,
          token,
          device.id,
          device.lanIps,
          tunnelServersForDevice(device),
        );
        const timeoutPromise = new Promise<never>((_, reject) =>
          setTimeout(() => reject(new Error("Could not connect in 20s")), 20000)
        );
        await Promise.race([connectPromise, timeoutPromise]);
        sendTelemetry(token, "connect-success", `Connected via ${quicClient.connectionMode}`, JSON.stringify({
          device: device.name, path: quicClient.connectionPath, network: quicClient.networkType, mode: quicClient.connectionMode,
        }));
        setConnectionStatus("connected");
        setLastError(null);
        setAgentAuthExpired(quicClient.agentAuthExpired);
        clearDeviceUnreachable(device.id);
        // Fetch hwid from /info for dedup (P2P only, never sent to Convex)
        try {
          const info = await quicClient.getInfo();
          if (info && (info as any).hwid) {
            const hwid = (info as any).hwid as string;
            setActiveDevice((prev) => prev ? { ...prev, hwid } : prev);
            setDevices((prev) => prev.map((d) => d.id === device.id ? { ...d, hwid } : d));
          }
        } catch {
          // Best-effort — hwid fetch failure is not fatal
        }
      } catch (e) {
        const errMsg = e instanceof Error ? e.message : String(e);
        sendTelemetry(token, "connect-fail", `Connection failed: ${errMsg}`, JSON.stringify({
          host: device.host, port: device.port, deviceId: device.id.slice(0, 8),
          relayCount: quicClient.relayServerCount,
        }));
        // Stop any background reconnection attempts
        quicClient.disconnect();
        setConnectionStatus("disconnected");
        setAgentAuthExpired(false);
        // Keep activeDevice so Retry button works — don't null it
        setLastError(errMsg);
        // Mark this specific device as confirmed-unreachable. The UI
        // consumes unreachableSet to show a stable "STALE / OFFLINE"
        // badge with an explicit retry, instead of flickering between
        // "online" (Convex says so) and "failed" (we just tried).
        // Cleared automatically when a future connect succeeds via
        // clearDeviceUnreachable, or when the user explicitly retries.
        markDeviceUnreachable(device.id);
      }
    },
    [token]
  );

  const disconnect = useCallback(() => {
    quicClient.disconnect();
    setActiveDevice(null);
    setConnectionStatus("disconnected");
    setUserDisconnected(true);
    setAgentAuthExpired(false);
  }, []);

  const setPrimaryDevice = useCallback(async (deviceId: string | null) => {
    if (!token) throw new Error("Not signed in");
    // Optimistic local update so the UI reflects the choice immediately.
    setPrimaryDeviceIdState(deviceId);
    try {
      // `null` sentinel tells Convex to clear the preference; omitting the
      // field leaves it untouched, which is the wrong semantics here.
      await saveUserSettings(token, { primaryDeviceId: deviceId });
    } catch (e) {
      // Roll back so the user sees the real state.
      appLog("error", `[settings] setPrimaryDevice failed: ${e}`);
      setPrimaryDeviceIdState((prev) => prev);
      throw e;
    }
  }, [token]);

  const stopReconnectAndBounce = useCallback(async () => {
    const failed = activeDevice;
    try {
      quicClient.stopReconnect();
    } catch {
      // best-effort
    }
    quicClient.disconnect();
    if (failed) {
      markDeviceUnreachable(failed.id);
    }
    setActiveDevice(null);
    setConnectionStatus("disconnected");
    setUserDisconnected(true);
    setAgentAuthExpired(false);
    setLastError(null);
    try {
      await refreshDevices();
    } catch {
      // refreshDevices already logs; never block the UI reset on it
    }
  }, [activeDevice, markDeviceUnreachable, refreshDevices]);

  const handleDetachDevice = useCallback(async (device: Device) => {
    const key = deviceIdentityKey(device);
    await addDetachedDevice(key);
    // If detaching the active device, disconnect first
    if (activeDevice?.id === device.id) {
      quicClient.disconnect();
      setActiveDevice(null);
      setConnectionStatus("disconnected");
    }
    setDevices((prev) => prev.filter((d) => deviceIdentityKey(d) !== key));
  }, [activeDevice]);

  const handleRemoveDevice = useCallback(async (device: Device) => {
    if (!token) throw new Error("Not signed in");
    if (device.isGuest) {
      await handleDetachDevice(device);
      return;
    }
    const res = await fetch(`${CONVEX_SITE_URL}/devices/remove`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ deviceId: device.id }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data.error || "Failed to remove device");
    }
    if (activeDevice?.id === device.id) {
      quicClient.disconnect();
      setActiveDevice(null);
      setConnectionStatus("disconnected");
      setAgentAuthExpired(false);
    }
    setDevices((prev) => prev.filter((d) => d.id !== device.id));
  }, [activeDevice, handleDetachDevice, token]);

  // Sync DeviceContext state with QUIC client's internal state changes
  // (e.g., polling failures trigger reconnection inside the QUIC client)
  useEffect(() => {
    const unsub = quicClient.on("connectionState", (state) => {
      // Only sync if we have an active device (i.e., we initiated a connection)
      if (!activeDevice) return;

      if (state === "connected") {
        setConnectionStatus("connected");
        setLastError(null);
        setAgentAuthExpired(quicClient.agentAuthExpired);
      } else if (state === "connecting") {
        setConnectionStatus("connecting");
      } else if (state === "error") {
        const attempt = quicClient.reconnectAttempt;
        const max = quicClient.maxReconnectAttempts;
        const gaveUp = attempt >= max || quicClient.reconnectStopped;
        if (gaveUp) {
          quicClient.disconnect();
          setConnectionStatus("disconnected");
          setAgentAuthExpired(false);
          setLastError(
            quicClient.reconnectStopped
              ? "Reconnection stopped"
              : `Could not reach device after ${max} attempts`,
          );
        } else {
          setConnectionStatus("error");
          setLastError(`Reconnecting (${attempt}/${max})...`);
        }
      } else if (state === "disconnected") {
        // QUIC client fully disconnected (e.g., via disconnect() call)
        // Don't clear activeDevice here — that's handled by the disconnect() callback
      }
    });
    return () => unsub();
  }, [activeDevice]);

  // Mirror auth-expiry changes from the transport into React state.
  // The QUIC client updates this flag from /health probes, but it is
  // not itself reactive.
  useEffect(() => {
    if (!activeDevice) {
      setAgentAuthExpired(false);
      return;
    }
    const iv = setInterval(() => {
      const next = quicClient.agentAuthExpired;
      setAgentAuthExpired((prev) => (prev === next ? prev : next));
    }, 1000);
    return () => clearInterval(iv);
  }, [activeDevice?.id]);

  // Auto-clear the "needs manual auth" block when the device list shows
  // the machine is no longer in bootstrap mode — i.e. the user ran
  // `yaver auth` on it directly. Without this the block would stick for
  // the whole session and UI would keep showing the soft banner after
  // the user already fixed it. Also clears the per-device attempt
  // counter so the auto-pair path doesn't immediately re-block.
  useEffect(() => {
    if (manualAuthRequiredSet.size === 0) return;
    let changed = false;
    const next = new Set(manualAuthRequiredSet);
    for (const blockedId of manualAuthRequiredSet) {
      const dev = devices.find((d) => d.id === blockedId);
      // Clear when either (a) the device is no longer in bootstrap mode
      // per Convex heartbeat, or (b) the device has disappeared from the
      // list entirely (removed by the user).
      if (!dev || dev.needsAuth !== true) {
        next.delete(blockedId);
        pairAttemptsRef.current.delete(blockedId);
        appLog("info", `[auto-pair] Cleared manual-auth block for ${blockedId}`);
        changed = true;
      }
    }
    if (changed) setManualAuthRequiredSet(next);
  }, [devices, manualAuthRequiredSet]);

  // Keep quicClient.token in sync with AuthContext — when Convex rotates
  // the session token (via X-Yaver-Rotate-Token), AuthContext persists it
  // and pushes the new value through React state. Without this hop, the
  // QUIC client keeps using the old bearer until the next reconnect and
  // every in-flight agent request fails 401 for ~30s until the token is
  // observed stale and a full reconnect is forced.
  useEffect(() => {
    if (!token) return;
    quicClient.setToken(token);
  }, [token]);

  // Fetch relay servers: local AsyncStorage > Convex user settings > Convex platform config
  // Extracted so it can be called on startup AND on reconnection (when relay list is empty).
  // Returns the number of relays ultimately loaded into the QUIC client — 0 means "no relays
  // from any source", which the startup retry loop uses as the trigger to back off and retry.
  const fetchRelayServers = useCallback(async (): Promise<number> => {
    try {
      // 1. Check for user-configured custom relays in local storage first
      const customRaw = await AsyncStorage.getItem(RELAYS_KEY);
      if (customRaw) {
        const customRelays: RelayServer[] = JSON.parse(customRaw);
        if (customRelays.length > 0) {
          quicClient.setRelayServers(customRelays);
          console.log("[DeviceContext] Using", customRelays.length, "custom relay server(s)");
          return customRelays.length;
        }
      }

      let platformServers: RelayServer[] = [];
      try {
        const res = await fetch(`${CONVEX_SITE_URL}/config`);
        if (res.ok) {
          const data = await res.json();
          platformServers = data.relayServers || [];
        }
      } catch {
        // Best-effort — account relay may still work on mobile without platform config.
      }

      // 2. No local relays — check Convex user settings (account-level relay config)
      if (token) {
        try {
          const settings = await getUserSettings(token);
          if (settings.relayUrl) {
            const resolved = resolveRelayServers(platformServers, settings.relayUrl, settings.relayPassword);
            quicClient.setRelayServers(resolved);
            // Persist the resolved fallback set so the app can reconnect offline too.
            await AsyncStorage.setItem(RELAYS_KEY, JSON.stringify(resolved));
            await AsyncStorage.setItem(SYNC_KEY, "true");
            console.log("[DeviceContext] Loaded", resolved.length, "relay server(s) from Convex user settings");
            return resolved.length;
          }
        } catch {
          // Best-effort — fall through to platform config
        }
      }

      // 3. No account-level relay — fall back to Convex platform config
      quicClient.setRelayServers(platformServers);
      console.log("[DeviceContext] Loaded", platformServers.length, "relay server(s) from Convex");
      return platformServers.length;
    } catch {
      sendTelemetry(token, "relays-failed", "Could not fetch relay config");
      return 0;
    }
  }, [token]);

  // Initial relay fetch on mount. If we end up with zero relays (likely a
  // transient `/config` fetch failure at boot — DNS not resolved yet,
  // airplane-mode toggle, cold tunnel), keep retrying in the background
  // with exponential backoff so the app recovers as soon as the network
  // is usable. Never blocks `setRelaysReady(true)` — the app can still
  // run on LAN-only connections while relays are being fetched.
  const relaysFetched = useRef(false);
  useEffect(() => {
    if (relaysFetched.current) return;
    relaysFetched.current = true;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const retryDelaysMs = [5_000, 10_000, 20_000, 40_000, 60_000];
    let attempt = 0;

    const tryFetch = async () => {
      const count = await fetchRelayServers();
      if (!cancelled) setRelaysReady(true);
      if (cancelled) return;
      if (count > 0) return;
      if (attempt >= retryDelaysMs.length) {
        console.log("[DeviceContext] No relays after retries — LAN-only connectivity");
        return;
      }
      const delay = retryDelaysMs[attempt++];
      console.log(`[DeviceContext] No relays loaded; retry in ${delay}ms`);
      timer = setTimeout(tryFetch, delay);
    };

    tryFetch();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [fetchRelayServers]);

  // Re-fetch relay config when connection is lost and relay list is empty.
  // This handles the case where relays weren't available at startup (e.g. settings
  // not yet seeded) but are now available after the backend created them on login.
  useEffect(() => {
    if (connectionStatus !== "error" && connectionStatus !== "disconnected") return;
    if (quicClient.relayServerCount > 0) return;
    console.log("[DeviceContext] Connection lost with no relays — re-fetching relay config");
    fetchRelayServers();
  }, [connectionStatus, fetchRelayServers]);

  // Load all user settings once on startup (single API call instead of 3+ separate ones)
  const settingsLoaded = useRef(false);
  useEffect(() => {
    if (!token || settingsLoaded.current) return;
    settingsLoaded.current = true;
    (async () => {
      try {
        const settings = await getUserSettings(token);

        // Apply forceRelay
        if (settings.forceRelay !== undefined) {
          quicClient.setForceRelay(settings.forceRelay);
          appLog("info", `[settings] forceRelay=${settings.forceRelay}`);
        }

        // Apply primary device preference (controls auto-connect when N > 1)
        if (settings.primaryDeviceId !== undefined) {
          setPrimaryDeviceIdState(settings.primaryDeviceId ?? null);
          appLog("info", `[settings] primaryDeviceId=${settings.primaryDeviceId ?? "(none)"}`);
        }

        // Apply tunnel from settings (if no local override)
        if (settings.tunnelUrl) {
          const customRaw = await AsyncStorage.getItem(TUNNELS_KEY);
          if (!customRaw || JSON.parse(customRaw).length === 0) {
            const accountTunnel: TunnelServer = {
              id: "account",
              url: settings.tunnelUrl,
              priority: 1,
            };
            quicClient.setTunnelServers([accountTunnel]);
            await AsyncStorage.setItem(TUNNELS_KEY, JSON.stringify([accountTunnel]));
            console.log("[DeviceContext] Loaded tunnel from Convex user settings:", settings.tunnelUrl);
          }
        }
      } catch {
        // Best-effort — settings will use defaults
      }
    })();
  }, [token, TUNNELS_KEY]);

  // One-time relay onboarding alert after first login
  const onboardingChecked = useRef(false);
  useEffect(() => {
    // Relay setup is now handled in the onboarding survey — no popup needed.
    if (!token || !relaysReady || onboardingChecked.current) return;
    onboardingChecked.current = true;
    AsyncStorage.setItem(ONBOARDING_KEY, "1").catch(() => {});
  }, [token, relaysReady]);

  // Start/stop LAN beacon listener based on auth state
  useEffect(() => {
    if (user?.id) {
      beaconListener.setUserId(user.id).then(() => {
        beaconListener.start();
      });
    }
    return () => {
      beaconListener.stop();
    };
  }, [user?.id]);

  // Feed known device IDs to beacon listener for matching
  useEffect(() => {
    if (devices.length > 0) {
      beaconListener.setKnownDevices(devices.map((d) => d.id));
    }
  }, [devices]);

  // When beacon discovers/loses a device, update device list
  useEffect(() => {
    const unsubDiscover = beaconListener.onDiscovered((discovered) => {
      const matchedIds: string[] = [];
      setDevices((prev) =>
        collapseAliasDevices(prev.map((d) => {
          if (
            d.id.startsWith(discovered.deviceId) ||
            (!!discovered.hwid && d.hwid === discovered.hwid) ||
            normalizedDeviceName(d.name) === normalizedDeviceName(discovered.name)
          ) {
            matchedIds.push(d.id);
            return { ...d, host: discovered.ip, port: discovered.port, online: true, local: true, hwid: discovered.hwid || d.hwid };
          }
          return d;
        }))
      );
      if (matchedIds.length > 0) {
        setUnreachableSet((prev) => {
          let changed = false;
          const next = new Set(prev);
          for (const id of matchedIds) {
            if (next.delete(id)) changed = true;
          }
          return changed ? next : prev;
        });
      }
      sendTelemetry(token, "peer-matched", `${discovered.name} at ${discovered.ip}:${discovered.port}`, discovered.deviceId);
    });

    const unsubLost = beaconListener.onLost((deviceId) => {
      setDevices((prev) =>
        prev.map((d) => {
          if (d.id.startsWith(deviceId)) {
            return { ...d, local: false };
          }
          return d;
        })
      );
      sendTelemetry(token, "peer-lost", `Device ${deviceId} beacon lost`);
    });

    return () => { unsubDiscover(); unsubLost(); };
  }, [token]);

  // Auto-pair bootstrap devices: when the beacon discovers a device
  // with needsAuth=true, try to authenticate it from the phone.
  //
  // Encrypted path (preferred): look up the device's X25519 public
  // key from Convex (registered during the first `yaver auth`) and
  // encrypt the token with NaCl box before sending over HTTP. An
  // attacker on the same WiFi cannot read the token even if they
  // intercept the request — only the real Mac's private key can
  // decrypt it.
  //
  // Passkey fallback: if the device has no public key in Convex
  // (never authed before), fall back to the legacy passkey flow
  // which requires the user to have run `yaver auth` at least once.
  const autoPairedRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!token || !user?.id) return;
    const iv = setInterval(async () => {
      const bootstraps = beaconListener.getBootstrapDevices();
      for (const dev of bootstraps) {
        if (autoPairedRef.current.has(dev.deviceId)) continue;
        if (isAutoPairBlocked(dev.deviceId)) continue;
        autoPairedRef.current.add(dev.deviceId);
        const targetUrl = `http://${dev.ip}:${dev.port}`;
        let paired = false;
        try {
          // Try encrypted path: find the device's public key from Convex.
          // If the beacon also broadcasts a public key (dpk), verify it
          // matches Convex — a mismatch means the device was reinstalled
          // and we should fall back to passkey.
          const knownDevice = devices.find(
            (d) => d.id.startsWith(dev.deviceId) || (dev.hwid && d.hwid === dev.hwid)
          );
          let pubKey = knownDevice?.publicKey;
          if (pubKey && dev.devicePublicKey && pubKey !== dev.devicePublicKey) {
            // Key mismatch: device was reinstalled with new keys but
            // Convex still has the old key. Skip encrypted path — the
            // next `yaver auth` will re-sync. Fall through to passkey.
            pubKey = undefined;
          }
          if (pubKey) {
            const res = await submitEncryptedPair(targetUrl, token, pubKey, dev.bootstrapPasskey);
            if (res.ok) {
              appLog("info", `Encrypted auto-pair: ${dev.name || dev.deviceId} at ${dev.ip}`);
              paired = true;
              recordAutoPairSuccess(dev.deviceId);
              setTimeout(() => refreshDevices(), 3000);
              continue;
            }
          }
          // Fallback: legacy passkey flow (requires passkey in beacon)
          if (!dev.bootstrapPasskey) continue;
          const info = await fetchPairInfo(targetUrl);
          if (!info.ok) continue;
          const res = await submitPair({
            code: dev.bootstrapPasskey,
            targetUrl,
            token,
            userId: user.id,
          });
          if (res.ok) {
            appLog("info", `Passkey auto-pair: ${dev.name || dev.deviceId} at ${dev.ip}`);
            paired = true;
            recordAutoPairSuccess(dev.deviceId);
            setTimeout(() => refreshDevices(), 3000);
          }
        } catch {
          autoPairedRef.current.delete(dev.deviceId);
        } finally {
          if (!paired) {
            autoPairedRef.current.delete(dev.deviceId);
            recordAutoPairFailure(dev.deviceId);
          }
        }
      }
    }, 3000);
    return () => clearInterval(iv);
  }, [token, user?.id, devices, refreshDevices, isAutoPairBlocked, recordAutoPairFailure, recordAutoPairSuccess]);

  // Relay auto-pair: probe known OFFLINE devices via relay to check
  // if they're in bootstrap mode. The bootstrap agent connects to the
  // relay with a placeholder token, so HTTP requests via the relay
  // still reach it. This covers the most common case: phone on 4G,
  // Mac at home with a wiped token.
  useEffect(() => {
    if (!token || !user?.id) return;
    const relays = quicClient.getRelayServers();
    if (relays.length === 0) return;

    const probed = new Set<string>();
    const iv = setInterval(async () => {
      // Target:
      //   - offline devices (may be in bootstrap but not yet re-registered)
      //   - online devices with needsAuth=true (re-registered via /devices/bootstrap)
      // Both need the same encrypted-pair flow via relay.
      const offlineDevices = devices.filter(
        (d) => (!d.online || d.needsAuth === true) && !d.isGuest && d.publicKey && !probed.has(d.id) && !autoPairedRef.current.has(d.id) && !isAutoPairBlocked(d.id)
      );
      for (const dev of offlineDevices) {
        probed.add(dev.id);
        const relayUrl = `${relays[0].httpUrl}/d/${dev.id}`;
        try {
          const infoRes = await fetch(`${relayUrl}/info`, { signal: AbortSignal.timeout(5000) });
          if (!infoRes.ok) continue;
          const info = await infoRes.json();
          if (!info.needsAuth) continue;
          if (!info.bootstrapPasskey) continue;

          autoPairedRef.current.add(dev.id);
          const res = await submitEncryptedPair(relayUrl, token, dev.publicKey!, info.bootstrapPasskey);
          if (res.ok) {
            appLog("info", `Relay encrypted auto-pair: ${dev.name} via ${relays[0].httpUrl}`);
            recordAutoPairSuccess(dev.id);
            setTimeout(() => refreshDevices(), 3000);
          } else {
            autoPairedRef.current.delete(dev.id);
            recordAutoPairFailure(dev.id);
          }
        } catch {
          // Device not reachable via relay — normal for truly offline devices.
          // Don't count as a failure: true offline doesn't mean the device
          // needs manual auth, it just isn't up right now.
        }
      }
    }, 15000);
    return () => clearInterval(iv);
  }, [token, user?.id, devices, refreshDevices, isAutoPairBlocked, recordAutoPairFailure, recordAutoPairSuccess]);

  // Bootstrap detection via direct connection. Covers the case where the
  // mobile app can reach the Mac's bootstrap HTTP server directly (LAN IP
  // known from Convex device list, no beacon needed). /info returns
  // { needsAuth: true } — mobile pushes its token immediately.
  // This is the catch-all for Release builds where UDP multicast may not
  // deliver beacons to the app, and for offline→bootstrap transitions
  // where the relay path would skip because the device appears online.
  useEffect(() => {
    if (!token || !user?.id || !activeDevice?.host) return;
    if (autoPairedRef.current.has(activeDevice.id)) return;
    if (isAutoPairBlocked(activeDevice.id)) return;
    const iv = setInterval(async () => {
      if (autoPairedRef.current.has(activeDevice.id)) return;
      if (isAutoPairBlocked(activeDevice.id)) return;
      try {
        const url = `http://${activeDevice.host}:${activeDevice.port || 18080}/info`;
        const res = await fetch(url, { signal: AbortSignal.timeout(3000) });
        if (!res.ok) return;
        const info = await res.json();
        if (!info.needsAuth) return;
        // Mark before we try so we don't retry-storm
        autoPairedRef.current.add(activeDevice.id);
        const targetUrl = `http://${activeDevice.host}:${activeDevice.port || 18080}`;
        // Try encrypted pair if we have this device's pubkey in Convex
        if (activeDevice.publicKey) {
          const ok = await submitEncryptedPair(targetUrl, token, activeDevice.publicKey, info.bootstrapPasskey || info.passkey);
          if (ok.ok) {
            appLog("info", `Direct encrypted auto-pair: ${activeDevice.name} at ${activeDevice.host}`);
            recordAutoPairSuccess(activeDevice.id);
            setTimeout(() => refreshDevices(), 3000);
            return;
          }
        }
        // Fallback: passkey pair — need to fetch passkey from bootstrap /info response
        const passkey = info.bootstrapPasskey || info.passkey;
        if (!passkey) {
          autoPairedRef.current.delete(activeDevice.id);
          recordAutoPairFailure(activeDevice.id);
          appLog("warn", `Bootstrap device ${activeDevice.name} has no passkey and no known pubkey — cannot auto-pair`);
          return;
        }
        const pairRes = await submitPair({
          code: passkey,
          targetUrl,
          token,
          userId: user.id,
        });
        if (pairRes.ok) {
          appLog("info", `Direct passkey auto-pair: ${activeDevice.name} at ${activeDevice.host}`);
          recordAutoPairSuccess(activeDevice.id);
          setTimeout(() => refreshDevices(), 3000);
        } else {
          autoPairedRef.current.delete(activeDevice.id);
          recordAutoPairFailure(activeDevice.id);
        }
      } catch {
        // Network error — will retry next tick. Do NOT count as an
        // auto-pair failure: the device may just be momentarily
        // unreachable, not broken.
      }
    }, 5000);
    return () => clearInterval(iv);
  }, [token, user?.id, activeDevice?.id, activeDevice?.host, activeDevice?.port, activeDevice?.publicKey, activeDevice?.name, refreshDevices, isAutoPairBlocked, recordAutoPairFailure, recordAutoPairSuccess]);

  const acceptGuestInvitation = useCallback(
    async (hostUserId: string, approvedDeviceIds?: string[]) => {
      if (!token) return;
      await apiAcceptInvitation(token, hostUserId, approvedDeviceIds);
      await refreshDevices();
    },
    [token, refreshDevices]
  );

  const acceptGuestByCode = useCallback(
    async (code: string, approvedDeviceIds?: string[]) => {
      if (!token) throw new Error("Not signed in");
      const result = await apiAcceptByCode(token, code, approvedDeviceIds);
      await refreshDevices();
      return result;
    },
    [token, refreshDevices]
  );

  const inviteGuest = useCallback(
    async (
      target: string | { email?: string; userId?: string; deviceIds?: string[] },
    ) => {
      if (!token) throw new Error("Not signed in");
      return await apiInviteGuest(token, target);
    },
    [token]
  );

  const recoverDeviceAuth = useCallback(async (device: Device): Promise<RecoveryResult | null> => {
    if (!token || !user?.id) {
      return { ok: false, error: "Not signed in" };
    }

    quicClient.primeTarget(
      device.host,
      device.port,
      token,
      device.id,
      device.lanIps,
      tunnelServersForDevice(device),
    );

    if (!activeDevice || activeDevice.id !== device.id) {
      try {
        await selectDevice(device);
      } catch (err) {
        appLog("warn", `Initial connect before auth recovery failed for ${device.name}: ${err instanceof Error ? err.message : String(err)}`);
      }
    }

    let recovery = await quicClient.recoverAgent(undefined, "pair");
    if (!recovery?.ok || !recovery.pairCode) {
      appLog("warn", `Host-token recovery did not open a pair session for ${device.name}: ${recovery?.error || "unknown error"}`);

      const bootstrapSecret = await getLocalSecret(LOCAL_KEYS.bootstrapSecret);
      if (bootstrapSecret) {
        recovery = await quicClient.recoverAgent(bootstrapSecret, "pair");
        if (recovery?.ok && recovery.pairCode) {
          appLog("info", `Recovered ${device.name} using stored bootstrap secret`);
        } else {
          appLog("warn", `Bootstrap-secret recovery did not open a pair session for ${device.name}: ${recovery?.error || "unknown error"}`);
        }
      }
    }

    if (!recovery?.ok || !recovery.pairCode) {
      const deviceCode = await quicClient.recoverAgent(undefined, "device-code");
      if (deviceCode?.ok && deviceCode.deviceCodeUrl) {
        appLog("info", `Opened device-code recovery for ${device.name}: ${deviceCode.userCode || "code unavailable"}`);
        Linking.openURL(deviceCode.deviceCodeUrl).catch(() => {});
      } else {
        appLog("warn", `Device-code recovery did not start for ${device.name}: ${deviceCode?.error || recovery?.error || "unknown error"}`);
      }
      return deviceCode ?? recovery;
    }

    const targetUrl = recovery.targetUrl || quicClient.baseUrl;
    let pairRes: { ok: boolean; host?: string; error?: string };
    if (device.publicKey) {
      pairRes = await submitEncryptedPair(targetUrl, token, device.publicKey, recovery.pairCode);
    } else {
      pairRes = await submitPair({
        code: recovery.pairCode,
        targetUrl,
        token,
        userId: user.id,
      });
    }
    if (!pairRes.ok) {
      appLog("warn", `Auth recovery pair submit failed for ${device.name}: ${pairRes.error || "unknown error"}`);
      return { ok: false, error: pairRes.error || "Auth recovery pair submit failed" };
    }
    quicClient.agentAuthExpired = false;
    setAgentAuthExpired(false);
    clearDeviceUnreachable(device.id);
    appLog("info", `Recovered expired agent session for ${device.name} from mobile`);
    setTimeout(() => refreshDevices(), 2000);
    return { ...recovery, ok: true };
  }, [token, user?.id, activeDevice, selectDevice, refreshDevices, clearDeviceUnreachable]);

  // Auth-expired recovery: the agent is still reachable, but its own
  // Convex session is stale. Use the PHONE'S valid bearer token to
  // authorize /auth/recover, open a one-shot pair session, then push
  // the token back immediately. This is the critical "remote box
  // rebooted, phone must recover it without SSH" path.
  const recoveringAuthRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!token || !user?.id || !activeDevice || !agentAuthExpired) return;
    if (recoveringAuthRef.current.has(activeDevice.id)) return;

    let cancelled = false;
    const tryRecover = async () => {
      if (cancelled || recoveringAuthRef.current.has(activeDevice.id)) return;
      recoveringAuthRef.current.add(activeDevice.id);
      try {
        await recoverDeviceAuth(activeDevice);
      } finally {
        if (!cancelled) {
          setTimeout(() => {
            recoveringAuthRef.current.delete(activeDevice.id);
          }, 5000);
        }
      }
    };

    tryRecover().catch((err) => {
      recoveringAuthRef.current.delete(activeDevice.id);
      // Surface the recovery failure so the user isn't stuck with a blank
      // "connection lost" banner — they at least know what to try next.
      const msg = err instanceof Error ? err.message : String(err);
      appLog("warn", `Auth recovery failed for ${activeDevice.name}: ${msg}`);
      if (!cancelled) {
        setLastError(`Auth recovery failed for ${activeDevice.name}: ${msg}. Sign in again from Settings or pick another device.`);
      }
    });

    return () => {
      cancelled = true;
    };
  }, [token, user?.id, activeDevice, agentAuthExpired, recoverDeviceAuth]);

  // Fetch devices when token becomes available + poll every 30s (lightweight)
  useEffect(() => {
    if (token) {
      refreshDevices();
      // Poll every 30s — beacon handles instant LAN discovery, this is just for online status
      const interval = setInterval(refreshDevices, 30000);
      return () => clearInterval(interval);
    } else {
      setDevices([]);
      setActiveDevice(null);
      setConnectionStatus("disconnected");
      setUserDisconnected(false);
    }
  }, [token, refreshDevices]);

  // Auto-connect rule (applies once, after login / relaysReady):
  //   1. Exactly one online device                 → auto-connect
  //   2. Multiple online, primaryDeviceId is one   → auto-connect the primary
  //   3. Multiple online, no (matching) primary    → force user to pick
  // The user-disconnect flag always wins so a manual "Stop" isn't overridden
  // by the auto-connect effect firing again.
  useEffect(() => {
    if (!token || !relaysReady || activeDevice || connectionStatus === "connecting" || userDisconnected) return;

    const recentDevices = devices.filter((d) => d.online);
    if (recentDevices.length === 0) return;

    let target: Device | null = null;
    let reason: "single" | "primary" = "single";
    if (recentDevices.length === 1) {
      target = recentDevices[0];
    } else if (primaryDeviceId) {
      const primary = recentDevices.find((d) => d.id === primaryDeviceId);
      if (primary) {
        target = primary;
        reason = "primary";
      }
    }

    if (target) {
      console.log(`[DeviceContext] Auto-connecting (${reason}) to`, target.name);
      sendTelemetry(token, "auto-connect", `${reason}: ${target.name}`, JSON.stringify({
        reason,
        relayCount: quicClient.relayServerCount,
        deviceId: target.id.slice(0, 8),
        onlineCount: recentDevices.length,
      }));
      selectDevice(target);

      // Seed primaryDeviceId after the first successful auto-connect on a
      // multi-device account. The single online device today becomes the
      // default for tomorrow — next launch skips the picker even if both
      // devices are online. User can always override in Settings.
      if (reason === "single" && devices.length > 1 && primaryDeviceId === null) {
        setPrimaryDevice(target.id).catch((e) => {
          appLog("warn", `[DeviceContext] Auto-set primaryDevice failed: ${e}`);
        });
      }
    }
    // Multiple devices + no primary (or primary offline) → do nothing; UI asks the user to pick.
  }, [devices, token, relaysReady, activeDevice, connectionStatus, userDisconnected, primaryDeviceId, selectDevice, setPrimaryDevice]);

  // Trigger immediate reconnection on network change (WiFi↔cellular roaming)
  useEffect(() => {
    let lastType: string | null = null;
    const unsubscribe = NetInfo.addEventListener((state) => {
      const currentType = state.type; // "wifi", "cellular", "none", etc.

      if (state.isConnected && activeDevice) {
        // Trigger full reconnect on network type change (WiFi → cellular, cellular → WiFi)
        // This clears stale relay URLs and re-probes all paths from scratch
        if (lastType && lastType !== currentType) {
          console.log(`[DeviceContext] Network changed: ${lastType} → ${currentType}`);
          sendTelemetry(token, "network-change", `${lastType} → ${currentType}`);
          quicClient.fullReconnect();
        } else if (!lastType) {
          // First event after mount or reconnection — just probe to be safe
          quicClient.triggerReconnect();
        }
      }
      lastType = currentType;
    });
    return () => unsubscribe();
  }, [activeDevice, token]);

  // When the app returns from a third-party app or the background,
  // resume the last active machine automatically instead of forcing
  // the user to tap the same device again. Also forwards foreground
  // state into the QUIC client so its reconnect loop pauses while
  // suspended (saves battery, no spurious "failed" state on resume).
  const appStateRef = useRef(AppState.currentState);
  useEffect(() => {
    const sub = AppState.addEventListener("change", (nextState: AppStateStatus) => {
      const prevState = appStateRef.current;
      appStateRef.current = nextState;
      quicClient.setForegroundState(nextState === "active");
      if (nextState !== "active" || !prevState.match(/inactive|background/)) return;
      if (!activeDevice || userDisconnected) return;
      if (quicClient.connectionState === "connected" || connectionStatus === "connecting") return;
      if (quicClient.reconnectAttempt > 0) {
        quicClient.triggerReconnect();
        return;
      }
      if (connectionStatus === "error" || connectionStatus === "disconnected") {
        quicClient.triggerReconnect();
        return;
      }
      selectDevice(activeDevice).catch(() => {});
    });
    return () => sub.remove();
  }, [activeDevice, connectionStatus, selectDevice, userDisconnected]);

  const value = useMemo<DeviceState>(
    () => ({
      devices,
      activeDevice,
      connectionStatus,
      isLoadingDevices,
      userDisconnected,
      lastError,
      agentAuthExpired,
      recoverDeviceAuth,
      selectDevice,
      disconnect,
      refreshDevices,
      detachDevice: handleDetachDevice,
      removeDevice: handleRemoveDevice,
      unreachableDeviceIds: Array.from(unreachableSet),
      markDeviceUnreachable,
      manualAuthRequiredDeviceIds: Array.from(manualAuthRequiredSet),
      stopReconnectAndBounce,
      guestInvitations,
      acceptGuestInvitation,
      acceptGuestByCode,
      inviteGuest,
      primaryDeviceId,
      setPrimaryDevice,
    }),
    [devices, activeDevice, connectionStatus, isLoadingDevices, userDisconnected, lastError, agentAuthExpired, recoverDeviceAuth, selectDevice, disconnect, refreshDevices, handleDetachDevice, handleRemoveDevice, unreachableSet, markDeviceUnreachable, manualAuthRequiredSet, stopReconnectAndBounce, guestInvitations, acceptGuestInvitation, acceptGuestByCode, inviteGuest, primaryDeviceId, setPrimaryDevice]
  );

  return <DeviceContext.Provider value={value}>{children}</DeviceContext.Provider>;
}

export function useDevice(): DeviceState {
  const ctx = useContext(DeviceContext);
  if (!ctx) {
    throw new Error("useDevice must be used within a DeviceProvider");
  }
  return ctx;
}
