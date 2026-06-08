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
import { appTag } from "../lib/appVersion";
import NetInfo from "@react-native-community/netinfo";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { router } from "expo-router";
import * as WebBrowser from "expo-web-browser";
import { quicClient, RecoveryResult, RelayServer, TunnelServer } from "../lib/quic";
import { connectionManager } from "../lib/connectionManager";
import { useAuth } from "./AuthContext";
import { getConvexSiteUrl, getLocalSecret, getUserSettings, saveUserSettings, LOCAL_KEYS } from "../lib/auth";
import { appLog } from "../lib/logger";
import { beaconListener, type DiscoveredDevice } from "../lib/beacon";
import { fetchPairInfo, submitPair } from "../lib/pairDevice";
import { submitEncryptedPair } from "../lib/encryptedPair";
import { probeMobileDeviceStatus } from "../lib/deviceStatus";
import { localBoxDeviceIfRunning } from "../lib/sandboxControl";
import { LOCAL_BOX_DEVICE_ID } from "../lib/localBox";
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
// HTTPS tunnel URLs from /devices/heartbeat publicEndpoints,
// per-device and authoritative. Deduplicated, stable order, host-wide
// tunnel last so per-device endpoints race first.
const DIRECT_HTTP_HOST_RE = /^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[0-1])\.|100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\.)/i;

function isUnsupportedCleartextPublicEndpoint(raw: string): boolean {
  try {
    const u = new URL(raw.trim());
    if (u.protocol !== "http:") return false;
    return !DIRECT_HTTP_HOST_RE.test(u.hostname);
  } catch {
    return false;
  }
}

function tunnelServersForDevice(device: Pick<Device, "id" | "name" | "tunnelUrl" | "publicEndpoints">): TunnelServer[] | undefined {
  const seen = new Set<string>();
  const out: TunnelServer[] = [];
  const add = (url: string, priority: number, label: string) => {
    const trimmed = url.trim().replace(/\/+$/, "");
    if (!trimmed || seen.has(trimmed)) return;
    // The tunnel/publicEndpoint stage is for HTTPS tunnels and other
    // browser/mobile-safe origins. Plain HTTP to public IPs is blocked
    // by iOS ATS and Android release cleartext policy, so trying it here
    // only delays relay fallback. LAN/tailnet HTTP still rides the
    // direct localIps path and is intentionally allowed.
    if (isUnsupportedCleartextPublicEndpoint(trimmed)) return;
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
        return {
          ...d,
          online: true,
          lastTunnelEvent: {
            online: true,
            at: Date.now(),
            connectedAt: typeof entry.connectedAt === "number" ? entry.connectedAt : undefined,
            durationSec: typeof entry.uptimeSec === "number" ? entry.uptimeSec : undefined,
          },
          lastSeen: Math.max(d.lastSeen || 0, Date.now()),
        };
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

// Default per-runner model used when the user changes runner without
// picking a specific model. Single source of truth — keep aligned with
// web/components/dashboard/DevicesView.tsx::DEFAULT_MODEL_BY_RUNNER and
// the agent's RunnerConfig.Model defaults in desktop/agent/tasks.go.
// Why hardcoded: the alternative is round-tripping
// /agent/runners → models lookup just to render the picker, which would
// add network latency to a UX flow that needs to feel instant.
// "opencode" intentionally has no entry — opencode picks its own
// internal default; clearing the per-device model is safer than
// inheriting Codex's gpt-5.4 or Claude's opus when switching to it.
export const DEFAULT_MODEL_BY_RUNNER: Record<string, string> = {
  claude: "claude-opus-4-7",
  // Codex-native model — general gpt-5.x error on a ChatGPT-account login
  // ("not supported when using Codex with a ChatGPT account").
  codex: "gpt-5.3-codex",
};

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
import { BUS_PRESENCE_STALE_MS, HEARTBEAT_STALE_MS } from "../_core/constants";

export interface RunnerInfo {
  taskId: string;
  runnerId: string;
  model?: string;
  pid: number;
  status: string;
  title: string;
  ready?: boolean;
}

export type DeviceConnectionPreferenceKind =
  | "direct-lan"
  | "tailscale"
  | "headscale"
  | "own-vpn"
  | "https-tunnel"
  | "free-relay"
  | "private-relay";

export interface DeviceConnectionPreference {
  kind: DeviceConnectionPreferenceKind;
  active: boolean;
  preferred: boolean;
  source: "agent-detected" | "user-config" | "platform-config" | "relay-presence";
}

export interface Device {
  id: string;
  name: string;
  /**
   * Per-user short alias (lower-cased server-side, unique within the
   * caller's own devices). Set via the inline editor on the device
   * card or `yaver alias set ...` from the CLI. Used by
   * `yaver ssh <alias>` and shown as "@alias" next to the name.
   */
  alias?: string;
  host: string;
  port: number;
  online: boolean;
  lastSeen: number;
  os: string;
  runners: RunnerInfo[];
  /** Durable inventory from Convex: which first-class coding CLIs are
   * installed on this device. Presence only, no auth state. */
  installedRunnerIds?: string[];
  /** X25519 public key (base64) for encrypted pairing — stored in Convex */
  publicKey?: string;
  /** true when the agent is running in bootstrap mode (no valid token) */
  needsAuth?: boolean;
  /** true when device is discovered via LAN beacon (same network) */
  local?: boolean;
  /** stable hardware ID (P2P only, never sent to Convex) */
  hwid?: string;
  /** Agent binary version reported via heartbeat or `/info` (e.g.
   *  "1.99.180"). Surfaced on machine-picker cards so the user can spot
   *  a box running an old daemon — the npm install might have bumped the
   *  on-disk symlink without the systemd unit ever picking it up. Empty
   *  when the row is too old to carry the field. */
  agentVersion?: string;
  /** Epoch ms when `agentVersion` was last reported; used to decide whether
   *  to fall back to a live `/info` probe instead of trusting the cached
   *  Convex value. */
  agentVersionReportedAt?: number;
  /** best-effort cached machine + local runtime capability snapshot */
  hardwareProfile?: {
    os?: string;
    osVersion?: string;
    cpu?: string;
    gpu?: string;
    ramMb?: number;
    vramMb?: number;
    numCores?: number;
    arch?: string;
    iosSimulators?: string[];
    androidEmulators?: string[];
  };
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
  peerState?: "online" | "stale" | "offline";
  peerLastSeen?: number;
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
  /** Convex-backed privacy-safe transport summary seeded by heartbeat:
   * free/private relay, own VPN, headscale/tailscale, LAN, HTTPS tunnel.
   * Concrete IPs/URLs remain in lanIps/publicEndpoints/relay config.
   */
  connectionPreferences?: DeviceConnectionPreference[];
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

/// Mirror the relay password the JS client just learned about into the
/// iOS UserDefaults that native swift panes (YaverFeedbackPane,
/// YaverAgentsPane) read via `yaverRelayHeaders()`. The native code
/// has no way to talk to /config or /settings on its own, so without
/// this push their /tasks + /runner-auth requests would land at the
/// relay without an X-Relay-Password header and 401 with
/// "Relay password mismatch".
///
/// Picks the first non-empty password in priority order:
///   1. quicClient.activeRelayPasswordValue (the in-flight value the
///      JS task path is actively using — most authoritative)
///   2. any per-server password attached to the resolved server list
///   3. account-level password from /settings (only the "user
///      customised relay" branch passes this)
function mirrorRelayPasswordToNative(
  servers: RelayServer[],
  accountRelayPassword?: string,
): void {
  try {
    const { NativeModules } = require("react-native");
    const fromQuic = quicClient.activeRelayPasswordValue;
    const fromServer = servers.find((r) => r.password)?.password;
    const relayPw =
      (fromQuic && fromQuic.trim()) ||
      (fromServer && fromServer.trim()) ||
      (accountRelayPassword && accountRelayPassword.trim()) ||
      "";
    NativeModules.YaverInfo?.setInheritedRelayPassword?.(relayPw);
  } catch {
    // Native module unavailable — non-iOS / unit-test path.
  }
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
  if (device.isGuest) {
    const hostScope = device.hostEmail || device.hostName || "guest";
    return `guest:${hostScope}:${device.id || device.name}`;
  }
  // Stable cryptographic identity wins. Without hwid or publicKey a
  // non-guest row is a "ghost": (platform, name) used to be the
  // fallback but it collided across fleets that share hostnames and
  // split single boxes across renames. Mark them as their own per-id
  // entry so reconnect can refuse to act on them.
  if (device.hwid) return `hwid:${device.hwid}`;
  if (device.publicKey) return `pub:${device.publicKey}`;
  if (device.id) return `id:${device.id}`;
  return `name:${device.name}`;
}

/** True when the device row has unstable identity (no hwid, no publicKey,
 *  not a guest). Reconnect, owner-claim, and reauth targets all need to
 *  block on this so a stale row doesn't accidentally match a live agent. */
export function isGhostDevice(device: Pick<Device, "hwid" | "publicKey" | "isGuest">): boolean {
  return !device.hwid && !device.publicKey && !device.isGuest;
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
      installedRunnerIds: incoming.installedRunnerIds?.length ? incoming.installedRunnerIds : existing.installedRunnerIds,
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
    installedRunnerIds: existing.installedRunnerIds?.length ? existing.installedRunnerIds : incoming.installedRunnerIds,
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

// PendingDeviceClaim mirrors the shape returned by /devices/pending-list.
// Bootstrap-pending boxes that joined the user's relay but have no
// Convex devices row yet — surfaced to the user so a freshly-installed
// remote box becomes claimable from the phone in one tap.
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

export interface DeviceState {
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
  /** Bootstrap-pending claims (boxes that joined the user's relay but
   *  have no Convex devices row yet). Surfaced so a fresh remote
   *  install is claimable in one tap from the phone. */
  pendingClaims: PendingDeviceClaim[];
  refreshPendingClaims: () => Promise<void>;
  claimPendingDevice: (deviceId: string, name?: string) => Promise<{ ok: boolean; error?: string }>;
  selectDevice: (device: Device) => Promise<void>;
  disconnect: () => void;
  refreshDevices: () => Promise<void>;
  detachDevice: (device: Device) => Promise<void>;
  removeDevice: (device: Device) => Promise<void>;
  /**
   * Set or clear the per-user alias for a device. Returns the
   * server-normalized alias on success (lower-cased). Server enforces
   * uniqueness — surface the returned error verbatim
   * ("alias already used …", "alias invalid …").
   */
  setDeviceAlias: (
    device: Device,
    alias: string,
  ) => Promise<{ ok: true; alias: string | null } | { ok: false; error: string }>;
  /** Device IDs the phone has failed to reach this session. Cleared on successful connect. */
  unreachableDeviceIds: string[];
  /** Flag a device as not reachable (e.g. after user hit Stop on a reconnect loop). */
  markDeviceUnreachable: (deviceId: string) => void;
  /** Devices where auto-pair has repeatedly failed; the user needs to run
   *  `yaver auth` on that machine manually. UI can surface a soft banner. */
  manualAuthRequiredDeviceIds: string[];
  /** Stop the active reconnect loop, clear the active device, mark it unreachable, and refresh from Convex. */
  stopReconnectAndBounce: () => Promise<void>;
  /** Re-run the reachability sweep + auto-pick (primary→secondary→alphabetical
   *  first reachable, else "Can't connect"). Wired to the Retry affordance. */
  retryConnection: () => void;
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
  /** Optional second elevated device. When primary is offline, the
   *  mobile auto-connect falls back to this one before showing the
   *  picker. `yaver ssh secondary` and the watchdog's tight 90s
   *  staleness threshold also apply. */
  secondaryDeviceId: string | null;
  /** Persist the secondary device. Pass null to clear. Same sync
   *  semantics as setPrimaryDevice. */
  setSecondaryDevice: (deviceId: string | null) => Promise<void>;
  /** Per-device primary coding agent. e.g. {"<deviceId>": "codex"}. The
   *  chat / task surfaces read this when opening a workspace and pre-
   *  select the runner so the user doesn't have to chase the pill on
   *  every reconnect. Mirrors the web dashboard's own dropdown. */
  primaryRunnerByDevice: Record<string, string>;
  /** Per-device model hint paired with the runner above. Optional.
   *  e.g. {"<deviceId>": "claude-opus-4-7"}. The agent forwards this
   *  to `--model` / `YAVER_CLAUDE_MODEL` / `YAVER_CODEX_MODEL` at
   *  spawn time so users can pick Opus-for-one-device / Sonnet-for-
   *  another without editing env vars. */
  primaryModelByDevice: Record<string, string>;
  /** Per-device OpenCode mode hint (`build` / `plan` / custom). */
  primaryModeByDevice: Record<string, string>;
  /** Per-device OpenCode provider hint (`zai`, `glm`, `ollama`, …). */
  primaryProviderByDevice: Record<string, string>;
  /** Persist a per-device primary coding agent + optional model/mode/provider.
   *  runnerId=null clears the entry. model=null clears just the model
   *  (runner stays). model=undefined leaves any existing model alone. */
  /** When true, the tasks `+` FAB opens a device + agent picker before
   *  the compose modal, letting one task target a non-active machine.
   *  Stored locally on the phone (no Convex roundtrip). Default false. */
  multiTargetMode: boolean;
  setMultiTargetMode: (enabled: boolean) => Promise<void>;
  setPrimaryRunnerForDevice: (
    deviceId: string,
    runnerId: string | null,
    model?: string | null,
    mode?: string | null,
    provider?: string | null,
  ) => Promise<void>;
  /** Latest published CLI/agent version (from /config). Null until
   *  the platform-config fetch returns. Pair this with a device's
   *  `agentVersion` to render an "outdated" badge on machine cards. */
  latestCliVersion: string | null;
  /** Device IDs whose pooled QuicClient is currently `isConnected`.
   *  Includes the focused device plus any background-connected boxes
   *  the user has previously selected this session. Drives the "N
   *  devices connected" badge and the multi-target wizard's
   *  "directly reachable" hint. */
  connectedDeviceIds: string[];
  /** Drop the pooled client for a single device without affecting
   *  any other live connections. Use this for an explicit
   *  "Disconnect from box X" UX — the equivalent of `disconnect()`
   *  in single-device mode, scoped to one machine. */
  disconnectDevice: (deviceId: string) => void;
}

const DeviceContext = createContext<DeviceState | undefined>(undefined);

/** Fire-and-forget telemetry to Convex + in-app logger (best-effort, never throws). */
function sendTelemetry(token: string | null, step: string, message: string, details?: string) {
  const level = step.includes("fail") ? "error" : "info";
  appLog(level as "info" | "error", `[${step}] ${message}${details ? " | " + details : ""}`);
  if (!_debugLogsEnabled) return;
  fetch(`${getConvexSiteUrl()}/mobile/log`, {
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
  const { token, user, notifyAuthFailure } = useAuth();
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
  // Sub-minute peer presence harvested from the connected agent's
  // P2P bus (`/bus/events`). Maps `deviceId` → unix-ms of the most
  // recent `peer/{id}/online|ping`. Lets the device list render
  // fresh even between Convex's 5-min `lastHeartbeat` updates: the
  // agent rings the bus every 60 s, so a healthy peer stays "fresh"
  // long before its Convex timestamp catches up.
  const [busPresence, setBusPresence] = useState<Record<string, number>>({});
  // Latest published CLI/agent version (from Convex platformConfig.cli_version
  // via /config). Used to render "X behind" badges on machine cards so the
  // user can spot a daemon that hasn't been restarted after npm bumped the
  // on-disk symlink. Null when /config hasn't returned yet.
  const [latestCliVersion, setLatestCliVersion] = useState<string | null>(null);
  // Device IDs whose pooled per-device client currently reports
  // `isConnected === true`. Updated whenever the connection manager
  // notifies (focus shift, pool add/remove) and on every QUIC
  // connection-state event from any pooled client. Surfaced so UI
  // can render "3 devices connected" without each consumer reaching
  // into the manager directly.
  const [connectedDeviceIds, setConnectedDeviceIds] = useState<string[]>([]);
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
  // Optional secondary slot. Auto-connect falls back here when primary
  // is offline; otherwise functions identically to primary (yaver ssh
  // secondary, tight watchdog threshold, etc).
  const [secondaryDeviceId, setSecondaryDeviceIdState] = useState<string | null>(null);
  // Per-device primary coding agent. Keyed by deviceId → runnerId.
  // Loaded from userSettings.primaryRunnerByDevice on mount, persisted
  // through saveUserSettings({primaryRunnerForDevice: …}). Empty for
  // fresh accounts; the dashboard / mobile picker seeds suggestions
  // and the user confirms.
  const [primaryRunnerByDevice, setPrimaryRunnerByDeviceState] = useState<Record<string, string>>({});
  // Per-device model hint alongside primaryRunnerByDevice. Same shape,
  // independent state so callers that only care about the runner id
  // don't have to re-render when the model changes and vice-versa.
  const [primaryModelByDevice, setPrimaryModelByDeviceState] = useState<Record<string, string>>({});
  const [primaryModeByDevice, setPrimaryModeByDeviceState] = useState<Record<string, string>>({});
  const [primaryProviderByDevice, setPrimaryProviderByDeviceState] = useState<Record<string, string>>({});
  // UI preference that follows the user across phones. Loaded from
  // userSettings on mount (see settings-load effect below) and
  // persisted through saveUserSettings on change.
  const [multiTargetMode, setMultiTargetModeState] = useState(false);
  const [settingsReady, setSettingsReady] = useState(false);
  const hasLoadedOnce = useRef(false);
  // Tracks the device the user most recently picked via the picker /
  // selectDevice. The split-brain auto-fallback below treats this as
  // sticky — it won't promote a different connected pool device on
  // top of a user's explicit selection unless the user explicitly
  // disconnects or picks something else. Fixes the "tap Mac mini in
  // picker, end up on yaver-test-ephemeral" bounce reported via
  // 2026-05-10 screen recording.
  const userSelectedDeviceIdRef = useRef<string | null>(null);
  // Reachability-driven auto-connect (see the effect below). `nonce` is the
  // re-trigger: the effect runs ONE ping-sweep attempt per nonce value, so it
  // never loops on a failing connect. Bump the nonce to re-run it — done on
  // device-set changes and on an explicit retry. `inFlight` guards against
  // overlapping sweeps; `attemptedNonce` records the last nonce we acted on.
  const autoConnectInFlightRef = useRef(false);
  const autoConnectAttemptedNonceRef = useRef(-1);
  const [autoConnectNonce, setAutoConnectNonce] = useState(0);

  const setMultiTargetMode = useCallback(async (enabled: boolean) => {
    setMultiTargetModeState(enabled);
    if (token) {
      await saveUserSettings(token, { multiTargetMode: enabled }).catch(() => {});
    }
  }, [token]);

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
      const convexSiteUrl = getConvexSiteUrl();
      // RN-Android: force a fresh TCP socket and bound the wait. After
      // OAuth deep-link bring-to-foreground, OkHttp's pool keeps sockets
      // that the upstream (iPhone Personal Hotspot, in our test setup)
      // has already dropped. Reusing one hangs send() forever — a 10 s
      // abort + cache-bust + Connection: close opens a fresh socket and
      // lets the user see a real error instead of a never-ending spinner.
      // iOS users were unaffected because NSURLSession's pool detects
      // dead sockets quickly; this matters most for Android.
      const controller = new AbortController();
      const abortTimer = setTimeout(() => controller.abort(), 10_000);
      let devicesRes: Response;
      try {
        devicesRes = await fetch(`${convexSiteUrl}/devices/list?_=${Date.now()}`, {
          headers: {
            Authorization: `Bearer ${token}`,
            "Cache-Control": "no-cache, no-store",
            Connection: "close",
          },
          signal: controller.signal,
        });
      } finally {
        clearTimeout(abortTimer);
      }
      appLog("info", `/devices/list status: ${devicesRes.status} via ${convexSiteUrl}`);

      if (devicesRes.ok) {
        const data = await devicesRes.json();
        const raw = data.devices || data || [];
        appLog("info", `Found ${raw.length} device(s) for ${user?.email || user?.id || "unknown-user"}`);
        const connectedDeviceId = quicClient.isConnected ? activeDevice?.id : null;
        const mapped: Device[] = raw.map((d: any) => {
          const deviceId = d.deviceId || d.id;
          // If we're actively connected to this device, trust our connection over stale heartbeat
          const isActivelyConnected = connectedDeviceId === deviceId;
          const lastTunnelEvent =
            d.lastTunnelEvent && typeof d.lastTunnelEvent === "object"
              ? {
                  online: Boolean(d.lastTunnelEvent.online),
                  at: typeof d.lastTunnelEvent.at === "number" ? d.lastTunnelEvent.at : 0,
                  peerAddr: typeof d.lastTunnelEvent.peerAddr === "string" ? d.lastTunnelEvent.peerAddr : undefined,
                  connectedAt: typeof d.lastTunnelEvent.connectedAt === "number" ? d.lastTunnelEvent.connectedAt : undefined,
                  durationSec: typeof d.lastTunnelEvent.durationSec === "number" ? d.lastTunnelEvent.durationSec : undefined,
                }
              : undefined;
          return {
            id: deviceId,
            name: d.isGuest ? `${d.name} (${d.hostName || "guest"})` : d.name,
            alias: typeof d.alias === "string" && d.alias.trim() !== "" ? d.alias : undefined,
            host: d.quicHost || d.host,
            port: d.quicPort || d.port,
            online: isActivelyConnected || (() => {
              const flag = d.isOnline ?? d.online ?? false;
              const lastSeen = d.lastHeartbeat || d.lastSeen || 0;
              const heartbeatFresh = flag && lastSeen > 0 && (Date.now() - lastSeen) < HEARTBEAT_STALE_MS;
              const relayLive =
                lastTunnelEvent &&
                lastTunnelEvent.online === true &&
                lastTunnelEvent.at > 0 &&
                (Date.now() - lastTunnelEvent.at) < HEARTBEAT_STALE_MS;
              return heartbeatFresh || relayLive;
            })(),
            lastSeen: isActivelyConnected ? Date.now() : (d.lastHeartbeat || d.lastSeen || 0),
            os: d.platform || d.os || "",
            runners: d.runners ?? [],
            installedRunnerIds: Array.isArray(d.installedRunnerIds) ? d.installedRunnerIds : undefined,
            publicKey: d.publicKey,
            hwid: d.hardwareId || d.hwid,
            agentVersion: typeof d.agentVersion === "string" && d.agentVersion.trim() !== ""
              ? d.agentVersion.trim()
              : undefined,
            agentVersionReportedAt: typeof d.agentVersionReportedAt === "number"
              ? d.agentVersionReportedAt
              : undefined,
            hardwareProfile: d.hardwareProfile ?? undefined,
            lanIps: Array.isArray(d.localIps) ? d.localIps : undefined,
            lastTunnelEvent,
            needsAuth: d.needsAuth ?? false,
            isGuest: d.isGuest || false,
            hostName: d.hostName,
            hostEmail: d.hostEmail,
            accessScope: d.accessScope,
            tunnelUrl: d.tunnelUrl,
            publicEndpoints: Array.isArray(d.publicEndpoints) ? d.publicEndpoints : undefined,
            connectionPreferences: Array.isArray(d.connectionPreferences) ? d.connectionPreferences : undefined,
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
        // Surface this phone's own on-device agent (Android sandbox) as a
        // selectable "This phone" box when it's running on loopback, so the
        // box picker / terminal / runner toggles can target it like any
        // remote machine. No-op (null) on iOS/web or when not started.
        const localBox = await localBoxDeviceIfRunning();
        const withLocalBox = localBox
          ? [localBox, ...finalDevices.filter((d) => d.id !== LOCAL_BOX_DEVICE_ID)]
          : finalDevices;
        setDevices(withLocalBox);
      } else {
        appLog("warn", `/devices/list failed: ${devicesRes.status} via ${convexSiteUrl}`);
        // A 401/403 here means our bearer is stale/rotated/revoked — but
        // the app still shows the cached account, so the user looks
        // "signed in" while every device query returns nothing. That
        // surfaces as an empty "No device connected / Disconnected"
        // screen with no hint that re-auth is needed (this is exactly how
        // a Mac that IS registered goes invisible on the phone). Route it
        // through the auth recovery: it rotates the token if the server
        // hands back a new one (next poll repopulates the list) or signs
        // the user out to the auth screen if the session was revoked.
        if (devicesRes.status === 401 || devicesRes.status === 403) {
          void notifyAuthFailure();
        }
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
  }, [token, user?.email, user?.id, notifyAuthFailure]);

  const selectDevice = useCallback(
    async (device: Device) => {
      if (!token) return;

      // Clear user-disconnect flag when user (or auto-connect) selects a device
      setUserDisconnected(false);
      setLastError(null);

      // Sticky user selection — pin this device id so the
      // split-brain auto-fallback effect (~line 1584) won't promote
      // a different pool device on top of an explicit user pick if
      // the picked device's connection has a brief blip mid-select.
      // Without this, the picker on the Reload tab would silently
      // bounce the user back to the previously-focused box: pick
      // Mac mini → Mac mini's pool client drops in the same render
      // window → connectionStatus flips to "error" → auto-fallback
      // picks yaver-test-ephemeral → user's selection vanishes.
      // Cleared by `disconnect()` and by an explicit selection of a
      // different device.
      userSelectedDeviceIdRef.current = device.id;

      // Multi-device: previously this tore the focused QuicClient down
      // before reconnecting it to a different deviceId, which dropped
      // every in-flight stream and forced peer-routed calls for every
      // non-focused box. Now we look up (or lazily create) a per-device
      // client in the connection manager pool, leave the previously
      // focused client alive in that pool so its tasks/streams keep
      // running, and just shift the "focused" pointer. Any code path
      // still using the legacy `quicClient` Proxy automatically follows
      // the new focus.
      const client = connectionManager.clientFor(device.id);
      connectionManager.setFocused(device.id);

      setConnectionStatus("connecting");
      setActiveDevice(device);
      setAgentAuthExpired(false);

      // If the per-device client already has a live connection (the
      // user is bouncing back to a box they recently were on), skip
      // the connect+timeout dance and just refresh state.
      if (client.isConnected) {
        sendTelemetry(token, "connect-resume", `Already connected to ${device.name}`, JSON.stringify({
          device: device.name, deviceId: device.id.slice(0, 8),
          mode: client.connectionMode,
        }));
        setConnectionStatus("connected");
        setLastError(null);
        setAgentAuthExpired(client.agentAuthExpired);
        clearDeviceUnreachable(device.id);
        return;
      }

      try {
        sendTelemetry(token, "connect-start", `Connecting to ${device.name}`, JSON.stringify({
          host: device.host, port: device.port, deviceId: device.id.slice(0, 8),
          relayCount: client.relayServerCount,
        }));
        // Race connect against a 20s timeout. Pass every reachable IP the
        // agent has reported in heartbeat (Wi-Fi LAN, Tailscale 100.x,
        // Ethernet) so the client can race them in parallel against the
        // beacon and Convex-stored primary host. Goes through
        // ensureConnected so a parallel boot-time warm-up attempt
        // and this user-driven attempt share one QuicClient.connect
        // call instead of trampling each other's relay/attempt state.
        const connectPromise = connectionManager.ensureConnected(device.id, {
          host: device.host,
          port: device.port,
          token,
          lanIps: device.lanIps,
          sessionTunnels: tunnelServersForDevice(device),
          connectionPreferences: device.connectionPreferences,
        });
        const timeoutPromise = new Promise<never>((_, reject) =>
          setTimeout(() => reject(new Error("Could not connect in 20s")), 20000)
        );
        await Promise.race([connectPromise, timeoutPromise]);
        sendTelemetry(token, "connect-success", `Connected via ${client.connectionMode}`, JSON.stringify({
          device: device.name, path: client.connectionPath, network: client.networkType, mode: client.connectionMode,
        }));
        setConnectionStatus("connected");
        setLastError(null);
        setAgentAuthExpired(client.agentAuthExpired);
        clearDeviceUnreachable(device.id);
        // Fetch hwid from /info for dedup (P2P only, never sent to Convex)
        try {
          const info = await client.getInfo();
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
          relayCount: client.relayServerCount,
        }));
        // Drop just THIS device's client. Any other clients in the pool
        // (boxes the user previously connected to) keep their state.
        connectionManager.disconnect(device.id);
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
    // User-initiated "Stop": tear down every pooled per-device client,
    // not just the focused one. Keeping a secondary connection alive
    // here would let it silently retry from the background and undo
    // the user's intent to fully disconnect. Sign-out re-enters the
    // same path via stopReconnectAndBounce/userDisconnected.
    connectionManager.disconnectAll();
    // Clear sticky-pick — an explicit disconnect releases the pin so
    // the next auto-fallback (after a re-sign-in or auto-pair) is
    // free to land on whichever device the user picks again.
    userSelectedDeviceIdRef.current = null;
    setActiveDevice(null);
    setConnectionStatus("disconnected");
    setUserDisconnected(true);
    setAgentAuthExpired(false);
  }, []);

  /** Drop a single device's pooled connection without affecting any
   *  other live ones. Used when a device row goes offline or the user
   *  explicitly removes one box from the running set. The focused
   *  client is replaced (focus clears) only if the dropped device WAS
   *  the focus — otherwise focus stays put. */
  const disconnectDevice = useCallback((deviceId: string) => {
    connectionManager.disconnect(deviceId);
    if (activeDevice?.id === deviceId) {
      setActiveDevice(null);
      setConnectionStatus("disconnected");
    }
  }, [activeDevice?.id]);

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

  const setSecondaryDevice = useCallback(async (deviceId: string | null) => {
    if (!token) throw new Error("Not signed in");
    setSecondaryDeviceIdState(deviceId);
    try {
      await saveUserSettings(token, { secondaryDeviceId: deviceId });
    } catch (e) {
      appLog("error", `[settings] setSecondaryDevice failed: ${e}`);
      setSecondaryDeviceIdState((prev) => prev);
      throw e;
    }
  }, [token]);

  const setPrimaryRunnerForDevice = useCallback(
    async (deviceId: string, runnerId: string | null, model?: string | null, mode?: string | null, provider?: string | null) => {
      if (!token) throw new Error("Not signed in");
      // When the runner changes (e.g. user picks Codex while the
      // previous pick was Claude with model "sonnet"), the stale
      // model is no longer compatible: codex spawned with
      // `--model sonnet` returns "The 'sonnet' model is not supported
      // when using Codex with a ChatGPT account." Auto-fill the new
      // runner's default model — single source of truth lives in
      // DEFAULT_MODEL_BY_RUNNER (mirrors web/DevicesView and the
      // agent's RunnerConfig.Model defaults). Caller can still pass
      // an explicit model to override, or `null` to clear.
      const previousRunner = primaryRunnerByDevice;
      const previousModel = primaryModelByDevice;
      const previousMode = primaryModeByDevice;
      const previousProvider = primaryProviderByDevice;
      const previousRunnerForThisDevice = previousRunner[deviceId] ?? "";
      const runnerChanged =
        !!runnerId && runnerId !== previousRunnerForThisDevice;
      let resolvedModel: string | null | undefined = model;
      if (resolvedModel === undefined && runnerChanged && runnerId) {
        const fallback = DEFAULT_MODEL_BY_RUNNER[runnerId];
        if (fallback) {
          resolvedModel = fallback;
          appLog(
            "info",
            `[settings] runner changed → ${runnerId}; auto-picking default model ${fallback}`,
          );
        } else {
          // Runner has no documented default (opencode etc.) — clear
          // any stale model so the agent falls through to the
          // runner's own internal default rather than re-using the
          // previous runner's incompatible model.
          resolvedModel = null;
        }
      }
      setPrimaryRunnerByDeviceState((prev) => {
        const next = { ...prev };
        if (runnerId) next[deviceId] = runnerId;
        else delete next[deviceId];
        return next;
      });
      setPrimaryModelByDeviceState((prev) => {
        const next = { ...prev };
        if (!runnerId || resolvedModel === null) {
          delete next[deviceId];
        } else if (
          typeof resolvedModel === "string" &&
          resolvedModel.length > 0
        ) {
          next[deviceId] = resolvedModel;
        }
        return next;
      });
      setPrimaryModeByDeviceState((prev) => {
        const next = { ...prev };
        if (!runnerId || mode === null) {
          delete next[deviceId];
        } else if (typeof mode === "string" && mode.length > 0) {
          next[deviceId] = mode;
        }
        return next;
      });
      setPrimaryProviderByDeviceState((prev) => {
        const next = { ...prev };
        if (!runnerId || provider === null) {
          delete next[deviceId];
        } else if (typeof provider === "string" && provider.length > 0) {
          next[deviceId] = provider;
        }
        return next;
      });
      try {
        await saveUserSettings(token, {
          primaryRunnerForDevice: {
            deviceId,
            runnerId,
            ...(resolvedModel !== undefined ? { model: resolvedModel } : {}),
            ...(mode !== undefined ? { mode } : {}),
            ...(provider !== undefined ? { provider } : {}),
          },
        });
      } catch (e) {
        appLog("error", `[settings] setPrimaryRunnerForDevice failed: ${e}`);
        setPrimaryRunnerByDeviceState(previousRunner);
        setPrimaryModelByDeviceState(previousModel);
        setPrimaryModeByDeviceState(previousMode);
        setPrimaryProviderByDeviceState(previousProvider);
        throw e;
      }
    },
    [token, primaryRunnerByDevice, primaryModelByDevice, primaryModeByDevice, primaryProviderByDevice],
  );

  const stopReconnectAndBounce = useCallback(async () => {
    const failed = activeDevice;
    try {
      quicClient.stopReconnect();
    } catch {
      // best-effort
    }
    if (failed) {
      // Drop only THIS device's pooled client. Other connections the
      // user has open to peer machines must keep running — they're not
      // implicated in the reconnect failure.
      connectionManager.disconnect(failed.id);
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
    // If detaching the active device, disconnect ITS pool client
    // (peer connections to other boxes stay open).
    if (activeDevice?.id === device.id) {
      connectionManager.disconnect(device.id);
      setActiveDevice(null);
      setConnectionStatus("disconnected");
    } else {
      // Even when not focused, the device may have a live pooled
      // connection from a previous focus — drop it so we don't leak.
      connectionManager.disconnect(device.id);
    }
    setDevices((prev) => prev.filter((d) => deviceIdentityKey(d) !== key));
  }, [activeDevice]);

  const handleSetDeviceAlias = useCallback(
    async (
      device: Device,
      alias: string,
    ): Promise<{ ok: true; alias: string | null } | { ok: false; error: string }> => {
      if (!token) return { ok: false, error: "Not signed in" };
      try {
        const res = await fetch(`${getConvexSiteUrl()}/devices/alias`, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ deviceId: device.id, alias }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          return { ok: false, error: body?.error || `HTTP ${res.status}` };
        }
        const next = body?.alias ?? null;
        // Optimistically update local state so the UI re-renders without
        // waiting for the next /devices/list poll.
        setDevices((prev) =>
          prev.map((d) => (d.id === device.id ? { ...d, alias: next ?? undefined } : d)),
        );
        return { ok: true, alias: next };
      } catch (e: any) {
        return { ok: false, error: e?.message || String(e) };
      }
    },
    [token],
  );

  const handleRemoveDevice = useCallback(async (device: Device) => {
    if (!token) throw new Error("Not signed in");
    if (device.isGuest) {
      await handleDetachDevice(device);
      return;
    }
    const res = await fetch(`${getConvexSiteUrl()}/devices/remove`, {
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
      connectionManager.disconnect(device.id);
      setActiveDevice(null);
      setConnectionStatus("disconnected");
      setAgentAuthExpired(false);
    } else {
      // Drop any background connection to the removed device so it
      // doesn't keep heartbeating to a Convex row that no longer
      // exists. Other pooled clients are untouched.
      connectionManager.disconnect(device.id);
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
          // Only the failed device's pooled client dies — peer
          // connections to other boxes the user is mid-session on
          // keep their state so a single flaky machine doesn't tear
          // down the whole multi-device experience.
          if (activeDevice) connectionManager.disconnect(activeDevice.id);
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

  // Subscribe to the connected agent's P2P bus so the device picker
  // sees sub-minute peer presence for the whole mesh — the bus pings
  // every 60 s while Convex `lastHeartbeat` only refreshes every
  // 5 min. Without this, a healthy peer would briefly look offline
  // between every Convex beat. Subscription is foreground-only;
  // iOS/Android kill the SSE socket within seconds of suspend, and
  // the AppState handler triggers a reconnect on resume which
  // re-fires this effect via `connectionStatus`.
  useEffect(() => {
    if (!activeDevice || connectionStatus !== "connected") {
      setBusPresence((prev) => (Object.keys(prev).length === 0 ? prev : {}));
      return;
    }
    const unsub = quicClient.subscribeBusEvents({
      prefix: "peer",
      onEvent: (evt) => {
        const segs = evt.topic.split("/");
        if (segs.length < 3 || segs[0] !== "peer") return;
        const peerId = segs[1];
        const kind = segs[2];
        if (!peerId) return;
        if (kind === "offline") {
          setBusPresence((prev) => {
            if (!(peerId in prev)) return prev;
            const next = { ...prev };
            delete next[peerId];
            return next;
          });
          return;
        }
        if (kind !== "online" && kind !== "ping") return;
        const at = evt.publishedAt > 0 ? evt.publishedAt : Date.now();
        setBusPresence((prev) => {
          if ((prev[peerId] || 0) >= at) return prev;
          return { ...prev, [peerId]: at };
        });
      },
      onError: () => {
        // Drop the SSE silently — the next connectionState transition
        // re-fires this effect and re-subscribes. We deliberately do
        // not clear `busPresence` on error: stale entries age out via
        // the freshness window on read.
      },
    });
    return () => unsub();
  }, [activeDevice?.id, connectionStatus]);

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

  // Mirror the active device's per-machine primary coding agent + model
  // (Convex source of truth: userSettings.primaryRunnerByDevice) into
  // iOS UserDefaults so the native YaverFeedbackPane reads the same
  // values the Tasks tab does. Without this, the shake-feedback flow
  // ignored the user's per-device pick and always defaulted to Claude
  // — which on root-running agents (remote test box) immediately
  // failed because Claude Code refuses --dangerously-skip-permissions
  // under uid 0. The mirror is best-effort: if YaverInfo isn't bound
  // (Android, simulator without the native module loaded, …) we skip
  // silently; the agent's pickReadyVibingRunner is the second line of
  // defense.
  useEffect(() => {
    try {
      const id = activeDevice?.id;
      const runner = id ? (primaryRunnerByDevice[id] ?? "") : "";
      const model = id ? (primaryModelByDevice[id] ?? "") : "";
      const { NativeModules } = require("react-native");
      NativeModules.YaverInfo?.setInheritedPrimaryRunner?.(runner, model);
    } catch {
      // Native module unavailable — non-iOS / unit-test path. Same
      // pattern as the relay-password mirror at line ~317.
    }
  }, [activeDevice?.id, primaryRunnerByDevice, primaryModelByDevice]);

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
    // Fan the new bearer out to every pooled per-device client so a
    // mid-session token rotation doesn't leave secondary connections
    // silently 401-ing for the rest of the session. The proxied
    // `quicClient.setToken` would only hit the focused client.
    connectionManager.setTokenOnAll(token);
  }, [token]);

  // Keep `connectedDeviceIds` in step with the pool. The manager fires
  // on focus + membership changes; we additionally poll every 4s so
  // the badge reflects the underlying QuicClient state changes (which
  // each client tracks via its own listener API but doesn't propagate
  // up to the manager). Cheap — it's just a Map iteration.
  useEffect(() => {
    const recompute = () => {
      const next = connectionManager.connectedDeviceIds();
      setConnectedDeviceIds((prev) => {
        if (prev.length === next.length && prev.every((id, i) => id === next[i])) return prev;
        return next;
      });
    };
    recompute();
    const unsub = connectionManager.subscribe(recompute);
    const interval = setInterval(recompute, 4000);
    return () => {
      unsub();
      clearInterval(interval);
    };
  }, []);

  // Keep the focused device pointed at a live pooled client. Without
  // this, the app can end up in a split-brain state: Devices shows
  // green CONNECTED cards from the pool, but activeDevice still points
  // at a stale box so Projects / Reload / other tabs think nothing is
  // connected. When that happens, promote a live pooled device to the
  // active focus immediately instead of waiting for the user to
  // manually re-select it.
  useEffect(() => {
    if (!settingsReady) return;
    if (userDisconnected || connectionStatus === "connecting") return;
    if (connectedDeviceIds.length === 0) return;
    if (activeDevice?.id && connectedDeviceIds.includes(activeDevice.id) && connectionStatus === "connected") return;

    // Sticky user pick: when the user explicitly selected a device
    // via the picker, don't stomp it just because that device's pool
    // client briefly dropped or its connect retry is mid-flight.
    // The split-brain promotion below would otherwise grab the next
    // pool device and bounce the user out of their pick — the exact
    // "I tap Mac mini, end up on yaver-test-ephemeral" symptom from
    // the 2026-05-10 screen recording. Only honour stickiness while
    // the picked device still exists in the device list (so a deletion
    // / sign-out still allows a fallback).
    const sticky = userSelectedDeviceIdRef.current;
    if (sticky && devices.some((d) => d.id === sticky)) {
      return;
    }

    const pickId =
      (primaryDeviceId && connectedDeviceIds.includes(primaryDeviceId) ? primaryDeviceId : null) ||
      (secondaryDeviceId && connectedDeviceIds.includes(secondaryDeviceId) ? secondaryDeviceId : null) ||
      connectedDeviceIds[0];
    if (!pickId) return;

    const picked = devices.find((d) => d.id === pickId);
    if (!picked) return;

    connectionManager.setFocused(pickId);
    const client = connectionManager.clientFor(pickId);
    setActiveDevice((prev) => (prev?.id === picked.id ? prev : picked));
    setConnectionStatus("connected");
    setLastError(null);
    setAgentAuthExpired(client.agentAuthExpired);
  }, [
    activeDevice?.id,
    connectedDeviceIds,
    connectionStatus,
    devices,
    primaryDeviceId,
    settingsReady,
    secondaryDeviceId,
    userDisconnected,
  ]);

  // Re-trigger auto-connect whenever the device set changes — a box that just
  // came online (or a freshly-paired one) deserves a fresh sweep.
  const deviceIdsKey = devices.map((d) => d.id).sort().join(",");
  useEffect(() => {
    setAutoConnectNonce((n) => n + 1);
  }, [deviceIdsKey]);

  // Manual retry: re-run the reachability sweep + auto-pick from scratch.
  // Wired to the banner/Tasks "Retry" affordance so a stuck "Connecting" or a
  // "Can't connect" can be re-attempted on demand.
  const retryConnection = useCallback(() => {
    setUserDisconnected(false);
    setAutoConnectNonce((n) => n + 1);
  }, []);

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
          connectionManager.setRelayServersOnAll(customRelays);
          mirrorRelayPasswordToNative(customRelays);
          console.log("[DeviceContext] Using", customRelays.length, "custom relay server(s)");
          return customRelays.length;
        }
      }

      let platformServers: RelayServer[] = [];
      try {
        const res = await fetch(`${getConvexSiteUrl()}/config`);
        if (res.ok) {
          const data = await res.json();
          platformServers = data.relayServers || [];
          if (typeof data.cliVersion === "string" && data.cliVersion.trim() !== "") {
            setLatestCliVersion(data.cliVersion.trim());
          }
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
            connectionManager.setRelayServersOnAll(resolved);
            // Persist the resolved fallback set so the app can reconnect offline too.
            await AsyncStorage.setItem(RELAYS_KEY, JSON.stringify(resolved));
            await AsyncStorage.setItem(SYNC_KEY, "true");
            mirrorRelayPasswordToNative(resolved, settings.relayPassword);
            console.log("[DeviceContext] Loaded", resolved.length, "relay server(s) from Convex user settings");
            return resolved.length;
          }
        } catch {
          // Best-effort — fall through to platform config
        }
      }

      // 3. No account-level relay — fall back to Convex platform config.
      // mirrorRelayPasswordToNative runs here too so accounts that use
      // the platform default relay (the common case — settings.relayUrl
      // is empty) still get an X-Relay-Password value into native
      // UserDefaults. Without this, every native pane request 401'd
      // with "invalid relay password" / "Relay password mismatch"
      // because the password lived only on the per-server entry
      // platformServers carries from /config.
      connectionManager.setRelayServersOnAll(platformServers);
      mirrorRelayPasswordToNative(platformServers);
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
          connectionManager.setForceRelayOnAll(settings.forceRelay);
          appLog("info", `[settings] forceRelay=${settings.forceRelay}`);
        }

        // Apply primary device preference (controls auto-connect when N > 1)
        if (settings.primaryDeviceId !== undefined) {
          setPrimaryDeviceIdState(settings.primaryDeviceId ?? null);
          appLog("info", `[settings] primaryDeviceId=${settings.primaryDeviceId ?? "(none)"}`);
        }
        if (settings.secondaryDeviceId !== undefined) {
          setSecondaryDeviceIdState(settings.secondaryDeviceId ?? null);
          appLog("info", `[settings] secondaryDeviceId=${settings.secondaryDeviceId ?? "(none)"}`);
        }

        // Multi-target mode is a UI preference that follows the user
        // across phones, so it lives on userSettings. undefined → off.
        if (settings.multiTargetMode !== undefined) {
          setMultiTargetModeState(!!settings.multiTargetMode);
          appLog("info", `[settings] multiTargetMode=${!!settings.multiTargetMode}`);
        }

        // Apply per-device primary coding agent preference. Stored on
        // userSettings as Array<{deviceId, runnerId}>; we fold it into
        // a flat map for cheap lookup. The chat / task surfaces read
        // primaryRunnerByDevice[activeDeviceId] when picking the
        // initial runner so users don't have to chase the runner pill.
        const rows = settings.primaryRunnerByDevice;
        if (Array.isArray(rows)) {
          const runners: Record<string, string> = {};
          const models: Record<string, string> = {};
          const modes: Record<string, string> = {};
          const providers: Record<string, string> = {};
          // Drop legacy / dead model identifiers when loading so a stale
          // selection from a previous app version doesn't keep forcing
          // the picker into a broken state. Codex CLI's old default
          // `o3-mini` 400s on ChatGPT-account auth and `gpt-5-codex`
          // was a transitional intermediate — both are now stripped so
          // preferredDefaultModelForRunner substitutes the current
          // default (`gpt-5.4`, OpenAI's latest GPT-5 release).
          const obsoleteModels = new Set(["o3-mini", "gpt-5-codex"]);
          for (const row of rows as Array<{ deviceId?: string; runnerId?: string; model?: string; mode?: string; provider?: string }>) {
            if (!row?.deviceId || !row?.runnerId) continue;
            runners[String(row.deviceId)] = String(row.runnerId);
            if (row.model && !obsoleteModels.has(String(row.model))) {
              models[String(row.deviceId)] = String(row.model);
            }
            if (row.mode) {
              modes[String(row.deviceId)] = String(row.mode);
            }
            if (row.provider) {
              providers[String(row.deviceId)] = String(row.provider);
            }
          }
          setPrimaryRunnerByDeviceState(runners);
          setPrimaryModelByDeviceState(models);
          setPrimaryModeByDeviceState(modes);
          setPrimaryProviderByDeviceState(providers);
          appLog("info", `[settings] primaryRunnerByDevice=${Object.keys(runners).length} entries, models=${Object.keys(models).length}, modes=${Object.keys(modes).length}, providers=${Object.keys(providers).length}`);
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
      } finally {
        setSettingsReady(true);
      }
    })();
  }, [token, TUNNELS_KEY]);

  // Backfill provider+model for opencode devices whose Convex row is
  // half-populated (runnerId only — happens when a user taps the
  // opencode runner pill before configuring it via OpenCodeConfigModal).
  // Reads opencode.json over the relay so the YaverInfo native mirror
  // (and any UI consumer) sees the device's actual model (e.g.
  // "zai/glm-4.7") instead of an empty string. The mirror at line ~1330
  // pushes the resolved model into iOS UserDefaults; without this
  // backfill the shake-feedback flow falls back to Claude on devices
  // where the user has actually configured opencode + GLM.
  const liveOpenCodeFetchedRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!quicClient.isConnected) return;
    let cancelled = false;
    (async () => {
      for (const [deviceId, runnerId] of Object.entries(primaryRunnerByDevice)) {
        if (runnerId !== "opencode") continue;
        if (primaryProviderByDevice[deviceId] || primaryModelByDevice[deviceId]) continue;
        if (liveOpenCodeFetchedRef.current.has(deviceId)) continue;
        liveOpenCodeFetchedRef.current.add(deviceId);
        try {
          const target = activeDevice?.id === deviceId ? undefined : deviceId;
          const cfg = await quicClient.getOpenCodeConfig(target);
          if (cancelled) return;
          const m = (cfg?.model || "").trim();
          if (!m) continue;
          const slash = m.indexOf("/");
          const provider = slash > 0 ? m.slice(0, slash) : "";
          setPrimaryModelByDeviceState((prev) =>
            prev[deviceId] === m ? prev : { ...prev, [deviceId]: m },
          );
          if (provider) {
            setPrimaryProviderByDeviceState((prev) =>
              prev[deviceId] === provider ? prev : { ...prev, [deviceId]: provider },
            );
          }
        } catch {
          // Device unreachable / opencode not installed — allow retry
          // on the next change tick.
          liveOpenCodeFetchedRef.current.delete(deviceId);
        }
      }
    })();
    return () => { cancelled = true; };
  }, [primaryRunnerByDevice, primaryProviderByDevice, primaryModelByDevice, activeDevice?.id]);

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

  // recoverBootstrapDevice handles the DIRECT-reach reclaim path: the agent's
  // bootstrap HTTP server is reachable on the LAN (or any network where /info
  // exposes its passkey), so we can run encrypted-pair or passkey-pair
  // straight against the agent.
  //
  // It deliberately does NOT iterate relay targets. The bootstrap server
  // suppresses bootstrapPasskey on /info whenever the request looks
  // proxied — X-Forwarded-For, X-Relay-Password, or non-LAN remote IP — so
  // the relay arm of this function would always fail at the "did not expose
  // a passkey" branch. The relay path is owner-claim's job; recoverDeviceAuth
  // dispatches to whichever fits the lifecycle probe.
  //
  // The optional `cachedInfo` lets the caller pass an already-fetched /info
  // payload to skip the duplicate round-trip when probeMobileDeviceStatus
  // already proved the device was direct-reachable.
  // ── Bootstrap-pending claims ────────────────────────────────────
  // Tracks boxes that registered themselves on the user's relay with
  // a `bootstrap-pending` token but have no Convex devices row yet.
  // The user's dashboard uses this to surface fresh installs that
  // would otherwise be invisible to anyone off-LAN.
  const [pendingClaims, setPendingClaims] = useState<PendingDeviceClaim[]>([]);
  const refreshPendingClaims = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${getConvexSiteUrl()}/devices/pending-list`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        // Older backends without the endpoint return 404 — keep
        // current state and try again next tick.
        return;
      }
      const data = await res.json().catch(() => ({}));
      const items: PendingDeviceClaim[] = Array.isArray(data?.items) ? data.items : [];
      setPendingClaims(items);
    } catch {
      // Network blip; keep prior state.
    }
  }, [token]);

  const claimPendingDevice = useCallback(
    async (deviceId: string, name?: string): Promise<{ ok: boolean; error?: string }> => {
      if (!token) return { ok: false, error: "Not signed in" };
      try {
        const res = await fetch(`${getConvexSiteUrl()}/devices/pending-claim`, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ deviceId, name }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({} as Record<string, unknown>));
          const msg = typeof body?.error === "string" ? body.error : `HTTP ${res.status}`;
          return { ok: false, error: msg };
        }
        // Pending row was deleted server-side and a real devices row
        // was created. Refresh both lists immediately so the UI
        // doesn't blink.
        await Promise.all([refreshPendingClaims(), refreshDevices()]);
        return { ok: true };
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : String(err) };
      }
    },
    [token, refreshPendingClaims, refreshDevices],
  );

  // Poll pending claims on the same cadence as devices (10s) so the
  // pending banner appears within one tick of a fresh install joining
  // the relay.
  useEffect(() => {
    if (!token) {
      setPendingClaims([]);
      return;
    }
    refreshPendingClaims();
    const iv = setInterval(refreshPendingClaims, 10000);
    return () => clearInterval(iv);
  }, [token, refreshPendingClaims]);

  const recoverBootstrapDevice = useCallback(async (
    device: Device,
    cachedInfo?: Record<string, any> | null,
  ): Promise<{ ok: boolean; targetUrl?: string; error?: string }> => {
    if (!token || !user?.id) return { ok: false, error: "Not signed in" };

    const port = device.port || 18080;
    const directTargets = Array.from(new Set([
      `http://${device.host}:${port}`,
      ...(device.lanIps || []).filter(Boolean).map((ip) => `http://${ip}:${port}`),
    ])).filter((url) => {
      try {
        const parsed = new URL(url);
        return !!parsed.hostname;
      } catch {
        return false;
      }
    });

    const tryPairAtUrl = async (
      targetUrl: string,
      info: Record<string, any>,
    ): Promise<{ ok: boolean; host?: string; error?: string } | { skip: true; reason: string }> => {
      const inBootstrap = info?.needsAuth === true || info?.mode === "bootstrap"
        || info?.lifecycleState === "bootstrap" || info?.lifecycle?.state === "bootstrap";
      if (!inBootstrap) {
        return { skip: true, reason: `${targetUrl} is up but not in bootstrap` };
      }
      const pairCode = info?.bootstrapPasskey || info?.passkey;
      if (device.publicKey) {
        const res = await submitEncryptedPair(targetUrl, token, device.publicKey, pairCode);
        // Encrypted pair without a code is rejected by the agent (400).
        // Without a passkey AND without success, fall through.
        if (res.ok) return res;
        if (!pairCode) return { skip: true, reason: `${targetUrl} did not expose a passkey for encrypted pair` };
        return res;
      }
      if (!pairCode) {
        return { skip: true, reason: `${targetUrl} did not expose a passkey and device has no publicKey` };
      }
      return await submitPair({ code: pairCode, targetUrl, token, userId: user.id });
    };

    let lastError = "bootstrap endpoint did not respond";

    // First, if the caller passed a fresh /info that already shows bootstrap
    // and a usable passkey, attempt against device.host without re-fetching.
    if (cachedInfo) {
      const primaryUrl = `http://${device.host}:${port}`;
      const out = await tryPairAtUrl(primaryUrl, cachedInfo);
      if ("ok" in out && out.ok) {
        quicClient.agentAuthExpired = false;
        setAgentAuthExpired(false);
        clearDeviceUnreachable(device.id);
        appLog("info", `Recovered bootstrap-mode Yaver auth for ${device.name} via cached /info`);
        setTimeout(() => refreshDevices(), 1200);
        return { ok: true, targetUrl: ("host" in out && out.host) || primaryUrl };
      }
      if ("skip" in out) {
        lastError = out.reason;
      } else if ("error" in out && out.error) {
        lastError = out.error;
      }
    }

    for (const targetUrl of directTargets) {
      try {
        const infoRes = await fetch(`${targetUrl}/info`, {
          signal: AbortSignal.timeout(3500),
        });
        if (!infoRes.ok) {
          lastError = `HTTP ${infoRes.status} from ${targetUrl}`;
          continue;
        }
        const info = await infoRes.json().catch(() => ({} as any));
        const out = await tryPairAtUrl(targetUrl, info);
        if ("ok" in out && out.ok) {
          quicClient.agentAuthExpired = false;
          setAgentAuthExpired(false);
          clearDeviceUnreachable(device.id);
          appLog("info", `Recovered bootstrap-mode Yaver auth for ${device.name} via ${targetUrl}`);
          setTimeout(() => refreshDevices(), 1200);
          return { ok: true, targetUrl: ("host" in out && out.host) || targetUrl };
        }
        if ("skip" in out) {
          lastError = out.reason;
        } else if ("error" in out && out.error) {
          lastError = out.error;
        }
      } catch (err) {
        lastError = err instanceof Error ? err.message : String(err);
      }
    }
    return { ok: false, error: lastError };
  }, [token, user?.id, refreshDevices, clearDeviceUnreachable]);

  const recoverDeviceAuth = useCallback(async (device: Device): Promise<RecoveryResult | null> => {
    if (!token || !user?.id) {
      return { ok: false, error: "Not signed in" };
    }

    // Ghost rows lack hwid/publicKey so any reconnect attempt would be
    // matching against unstable identity. Refuse early — let the user
    // re-pair from the box (or rely on the bootstrap-pending flow on a
    // truly fresh install) instead of silently hitting whatever row
    // happens to share a hostname.
    if (isGhostDevice(device)) {
      const msg = "Cannot recover: device row is missing identity (hwid/publicKey). Re-pair from the device.";
      appLog("warn", `${device.name}: ${msg}`);
      return { ok: false, error: msg };
    }

    let lifecycleProbe = await probeMobileDeviceStatus(device, token, 3500).catch(() => null);

    // Fully offline? The box's agent process is down, so neither the
    // safe-layer /auth/recover nor a direct /info probe can reach it —
    // every transport candidate just times out. Before giving up,
    // delegate a regular SSH recovery to an online peer: ask the
    // currently-connected agent to `yaver ssh <target>` into the box and
    // restart Yaver (the watchdog peer-recovery path). That ssh now
    // resolves the box's reachable LAN/tunnel route, so a phone — which
    // can't ssh itself — gets the "regular ssh" recovery for free. Once
    // the box heartbeats again we fall straight through to the normal
    // safe-layer re-auth cascade below (which is the `yaver auth
    // --headless` equivalent over the agent transport). No-op when no
    // online peer is connected (nothing can ssh on our behalf) or when
    // the box was reachable all along.
    if (!lifecycleProbe?.reachable) {
      const peer = await quicClient
        .recoverPeer(device.id)
        .catch((e) => ({ ok: false as const, error: e instanceof Error ? e.message : String(e) }));
      if (peer.ok) {
        appLog("info", `Asked an online peer to SSH-recover ${device.name}: ${peer.outcome}`);
        // Poll for the restarted agent to come back online (~24s budget).
        for (let i = 0; i < 8; i++) {
          await new Promise((r) => setTimeout(r, 3000));
          const reprobe = await probeMobileDeviceStatus(device, token, 3500).catch(() => null);
          if (reprobe?.reachable) {
            lifecycleProbe = reprobe;
            appLog("info", `${device.name} back online after peer SSH-recovery — continuing re-auth`);
            break;
          }
        }
      } else {
        appLog("warn", `Peer SSH-recovery unavailable for ${device.name}: ${(peer as { error?: string }).error}`);
      }
    }

    const lifecycleState = lifecycleProbe?.lifecycleState;
    const shouldTryBootstrap =
      lifecycleState === "bootstrap" || (!lifecycleState && device.needsAuth === true);

    if (shouldTryBootstrap) {
      // Route by transport. recoverBootstrapDevice can only succeed when
      // /info exposed a usable passkey, which the agent suppresses on
      // proxied requests (relay, X-Forwarded-For). When the probe found
      // the device only over relay, jump straight to owner-claim instead
      // of doing a round-trip we know will fail.
      const probedDirect = lifecycleProbe?.path === "direct" && !!lifecycleProbe.info;
      const probedRelay = lifecycleProbe?.path === "relay";

      if (probedDirect) {
        const bootstrapRecovery = await recoverBootstrapDevice(device, lifecycleProbe?.info);
        if (bootstrapRecovery.ok) {
          return { ok: true, targetUrl: bootstrapRecovery.targetUrl };
        }
        appLog("warn",
          `Direct bootstrap recovery failed for ${device.name}: ${bootstrapRecovery.error || "unknown"} — falling through to owner-claim`);
      }

      const claimed = await quicClient.ownerClaimDevice(device.id, {
        host: device.host,
        port: device.port,
        lanIps: device.lanIps,
        tunnelUrl: device.tunnelUrl,
        publicEndpoints: device.publicEndpoints,
      });
      if (claimed.ok) {
        quicClient.agentAuthExpired = false;
        setAgentAuthExpired(false);
        clearDeviceUnreachable(device.id);
        appLog("info", `Recovered bootstrap-mode Yaver auth for ${device.name} via owner claim`);
        setTimeout(() => refreshDevices(), 800);
        return { ok: true, targetUrl: claimed.host };
      }
      appLog("warn", `Owner-claim recovery failed for ${device.name}: ${claimed.error}`);

      // Last resort: if probe was direct but cached attempt failed, OR
      // probe was relay (so we never tried direct), try direct now.
      // Catches the case where probe ran over relay first but a direct
      // path is also reachable (e.g. LAN became available between probe
      // and reclaim).
      if (!probedDirect) {
        const fallbackDirect = await recoverBootstrapDevice(device);
        if (fallbackDirect.ok) {
          return { ok: true, targetUrl: fallbackDirect.targetUrl };
        }
        if (probedRelay) {
          appLog("warn",
            `Relay-probed bootstrap reclaim exhausted for ${device.name}: owner-claim and direct both failed`);
        }
      }
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

    // Try mode=direct first — single round-trip that hands this
    // mobile's bearer to the agent as its new token. Verified
    // end-to-end against the remote test box from a host-authed
    // CLI: 1 HTTP call flips lifecycleState from yaver-auth-expired
    // to ready-to-connect with no pair session, no OAuth, no user
    // interaction. The agent's /auth/recover requires the caller to
    // be host-authed (verifyHostToken: caller's userId owns the
    // device) which mobile always is when its own session is alive.
    // If direct fails (host check rejects, or this client's session
    // itself has died), we fall through to the pair / device-code
    // paths below — those still cover the off-LAN / brand-new-box /
    // multi-account scenarios.
    const directRecovery = await quicClient.recoverAgent(undefined, "direct");
    if (directRecovery?.ok) {
      quicClient.agentAuthExpired = false;
      setAgentAuthExpired(false);
      clearDeviceUnreachable(device.id);
      appLog("info", `Recovered ${device.name} via direct host-token push (1-call)`);
      setTimeout(() => refreshDevices(), 800);
      return { ok: true, targetUrl: directRecovery.targetUrl };
    }
    if (directRecovery?.alreadyHealthy) {
      // Agent's session is already valid — nothing to do, just clear our
      // stale flag and refresh. This happens when one client (auto-recovery
      // in this same app, or the web dashboard) recovered moments ago and
      // we're still showing the pre-recovery state.
      quicClient.agentAuthExpired = false;
      setAgentAuthExpired(false);
      clearDeviceUnreachable(device.id);
      setTimeout(() => refreshDevices(), 200);
      return { ok: true, targetUrl: directRecovery.targetUrl };
    }
    if (directRecovery?.rateLimited) {
      // Agent's per-IP recovery rate limiter (5s) just rejected us. Falling
      // back to pair / bootstrap-secret / device-code modes hits the SAME
      // /auth/recover endpoint and would just reproduce the 429 — the user
      // sees "too many recovery attempts" from a single tap. Surface the
      // signal up so the UI can show "wait a few seconds and retry"
      // instead of failing through 4 modes for nothing.
      appLog("warn", `Direct recovery rate-limited for ${device.name} — surface to caller`);
      return directRecovery;
    }
    appLog("warn", `Direct recovery rejected for ${device.name} (${directRecovery?.error || "unknown"}) — falling back to pair-session path`);

    // Direct mode failed for a non-rate-limit reason. Skip the legacy
    // pair-session cascade — both pair and bootstrap-secret-pair hit
    // the SAME /auth/recover endpoint and contribute another POST per
    // mode to the agent's 5s rate window. They've also been redundant
    // since direct host-token push landed: any caller who can pass
    // verifyHostToken (mobile signed in as the device owner) gets a
    // 1-call success in direct mode; if that fails, pair won't fix it.
    // Jump straight to device-code, which is the equivalent of running
    // `yaver primary auth` from the desktop CLI — opens a Convex OAuth
    // page in an in-app browser for explicit re-authorization.
    const deviceCode = await quicClient.recoverAgent(undefined, "device-code");
    if (deviceCode?.ok && deviceCode.deviceCodeUrl) {
      appLog("info", `Opened device-code recovery for ${device.name}: ${deviceCode.userCode || "code unavailable"}`);
      try {
        await WebBrowser.openBrowserAsync(deviceCode.deviceCodeUrl, {
          // iOS: dismiss button label inside the in-app Safari sheet
          dismissButtonStyle: "done",
          // Match the app's tone so the sign-in sheet doesn't feel like
          // a foreign tab.
          controlsColor: "#8b5cf6",
          presentationStyle: WebBrowser.WebBrowserPresentationStyle.PAGE_SHEET,
        });
      } catch (err) {
        // openBrowserAsync rejects on Android < API 18 / web — fall back
        // to the system browser so the user can still complete OAuth.
        appLog("warn", `WebBrowser open failed for ${device.name}, falling back to Linking: ${err instanceof Error ? err.message : String(err)}`);
        Linking.openURL(deviceCode.deviceCodeUrl).catch(() => {});
      }
      // Background-poll /auth/recover/session until the agent reports
      // recovered / expired / failed so the UI can flip the banner the
      // moment Convex confirms the new token, instead of relying on two
      // blind 1s/5s refreshes that often miss the heartbeat round-trip.
      // The endpoint is rate-limit-free (the GET handler doesn't hit
      // recoveryLimiter), so a 2s cadence is fine.
      if (deviceCode.recoveryId && deviceCode.waitToken) {
        const recoveryId = deviceCode.recoveryId;
        const waitToken = deviceCode.waitToken;
        const pollDeadline = Date.now() + 12 * 60 * 1000; // 12 min — device codes expire ≤10m
        let consecutiveFailures = 0;
        const tick = async () => {
          if (Date.now() > pollDeadline) {
            appLog("warn", `Device-code poll for ${device.name} timed out — falling back to refresh`);
            refreshDevices().catch(() => {});
            return;
          }
          const status = await quicClient.recoverSessionStatus(recoveryId, waitToken);
          if (!status) {
            // No transport available right now (agent may be flapping
            // mid-recovery). Back off and try again.
            consecutiveFailures += 1;
            if (consecutiveFailures > 6) {
              appLog("warn", `Device-code poll for ${device.name}: 6 transport failures, giving up`);
              refreshDevices().catch(() => {});
              return;
            }
            setTimeout(tick, 4000);
            return;
          }
          if (!status.ok) {
            // Agent answered but didn't recognize the session (404 etc.)
            // — no point retrying, the session is gone.
            appLog("warn", `Device-code poll for ${device.name}: ${status.error || "session lookup failed"}`);
            refreshDevices().catch(() => {});
            return;
          }
          consecutiveFailures = 0;
          switch (status.state) {
            case "recovered":
              appLog("info", `${device.name}: device-code recovery succeeded`);
              quicClient.agentAuthExpired = false;
              setAgentAuthExpired(false);
              clearDeviceUnreachable(device.id);
              refreshDevices().catch(() => {});
              setTimeout(() => refreshDevices().catch(() => {}), 3000);
              return;
            case "expired":
            case "failed":
              appLog("warn", `${device.name}: device-code recovery ended in ${status.state}${status.error ? ` (${status.error})` : ""}`);
              refreshDevices().catch(() => {});
              return;
            default:
              setTimeout(tick, 2000);
          }
        };
        // First tick fires fast — the user may have completed sign-in
        // on the same phone, race the WebBrowser dismissal.
        setTimeout(tick, 1500);
      } else {
        // Older agent that didn't return recoveryId/waitToken — fall
        // back to the legacy double-refresh.
        setTimeout(() => refreshDevices().catch(() => {}), 1000);
        setTimeout(() => refreshDevices().catch(() => {}), 5000);
      }
    } else {
      appLog("warn", `Device-code recovery did not start for ${device.name}: ${deviceCode?.error || "unknown error"}`);
    }
    return deviceCode;
  }, [token, user?.id, activeDevice, selectDevice, refreshDevices, clearDeviceUnreachable, recoverBootstrapDevice, setAgentAuthExpired]);

  // Auth-expired recovery: the agent is still reachable, but its own
  // Convex session is stale. Use the PHONE'S valid bearer token to
  // authorize /auth/recover, open a one-shot pair session, then push
  // the token back immediately. This is the critical "remote box
  // rebooted, phone must recover it without SSH" path.
  const recoveringAuthRef = useRef<Set<string>>(new Set());
  // Auto-recovery fail count per device per session. After 2 failures
  // we stop silently retrying — every state change that re-fired the
  // effect was burning the agent's 5s rate-limit window without any
  // user feedback, and made the manual Re-auth button race the silent
  // retries. After cap is hit, the banner + Re-auth button is the
  // only recovery path. Cleared on logout / app restart / successful
  // recovery.
  const autoRecoveryFailRef = useRef<Map<string, number>>(new Map());
  const AUTO_RECOVERY_MAX_FAILS = 2;
  // Set of device IDs we've already nagged about Yaver auth this
  // session. Cleared on logout / app restart. Prevents the auto-
  // guide Alert from re-firing on every 30s heartbeat poll.
  const guideShownRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!token || !user?.id || !activeDevice || !agentAuthExpired) return;
    if (recoveringAuthRef.current.has(activeDevice.id)) return;
    // Cap reached — wait for the user to tap Re-auth manually.
    if ((autoRecoveryFailRef.current.get(activeDevice.id) ?? 0) >= AUTO_RECOVERY_MAX_FAILS) {
      return;
    }
    // Silent recovery is gated to the primary device only. Other devices
    // (including non-primary owned + guest-shared) require an explicit
    // user tap. Avoids hammering the agent's rate-limit budget on devices
    // the user isn't actively trying to use.
    if (!primaryDeviceId || primaryDeviceId !== activeDevice.id) return;
    // And only when the device is up — no point burning our 2-attempt
    // budget against a box that's offline or stale.
    const recentTunnel = activeDevice.lastTunnelEvent && activeDevice.lastTunnelEvent.online &&
      Date.now() - activeDevice.lastTunnelEvent.at < 90_000;
    const peerOnline = activeDevice.peerState === "online";
    const isUp = peerOnline || recentTunnel || activeDevice.online;
    if (!isUp) return;

    let cancelled = false;
    const tryRecover = async () => {
      if (cancelled || recoveringAuthRef.current.has(activeDevice.id)) return;
      recoveringAuthRef.current.add(activeDevice.id);
      try {
        const result = await recoverDeviceAuth(activeDevice);
        if (result?.ok) {
          autoRecoveryFailRef.current.delete(activeDevice.id);
        } else {
          // Anything not-OK (including rateLimited) counts toward the cap so
          // the auto loop eventually yields to the user. The user's manual
          // tap can still fire — it goes through a separate code path that
          // doesn't read this counter.
          const prev = autoRecoveryFailRef.current.get(activeDevice.id) ?? 0;
          autoRecoveryFailRef.current.set(activeDevice.id, prev + 1);
        }
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
      // Exceptional path also counts toward the cap.
      const prev = autoRecoveryFailRef.current.get(activeDevice.id) ?? 0;
      autoRecoveryFailRef.current.set(activeDevice.id, prev + 1);
      // Surface the recovery failure so the user isn't stuck with a blank
      // "connection lost" banner — they at least know what to try next.
      const msg = err instanceof Error ? err.message : String(err);
      appLog("warn", `Auth recovery failed for ${activeDevice.name}: ${msg}`);
      if (!cancelled) {
        setLastError(`Auth recovery failed for ${activeDevice.name}: ${msg}. Sign in again from Settings or pick another device.`);
        // Auto-guide the user to the per-device Recovery UI in Device
        // Details. The auto-recovery loop above tried silently; if it
        // failed, the user needs the in-Modal "Recover Yaver Auth"
        // button (which uses the smart dispatcher and surfaces the
        // detailed error). Only prompt once per device per session
        // — guideShownRef prevents Alert spam if the polling loop
        // re-detects the same auth-expired state every 30s.
        if (!guideShownRef.current.has(activeDevice.id)) {
          guideShownRef.current.add(activeDevice.id);
          const dev = activeDevice;
          setTimeout(() => {
            if (cancelled) return;
            Alert.alert(
              `${dev.name} needs Yaver auth`,
              `The agent's session expired and the auto-recovery couldn't refresh it. Open the device details to run a manual recover, or dismiss to handle later.\n\n${appTag()}`,
              [
                { text: "Later", style: "cancel" },
                {
                  text: "Open recovery",
                  onPress: () => {
                    router.push({
                      pathname: "/(tabs)/devices",
                      params: { openDetails: dev.id, focus: "recovery" },
                    } as any);
                  },
                },
              ],
              { cancelable: true },
            );
          }, 800);
        }
      }
    });

    return () => {
      cancelled = true;
    };
  }, [token, user?.id, activeDevice, agentAuthExpired, recoverDeviceAuth, primaryDeviceId]);

  // Fetch devices when token becomes available + poll every 30s (lightweight)
  useEffect(() => {
    if (token) {
      refreshDevices();
      // Poll every 30s — beacon handles instant LAN discovery, this is just for online status
      const interval = setInterval(refreshDevices, 30000);
      return () => clearInterval(interval);
    } else {
      // Token cleared = signed out. Tear down every pooled per-device
      // client so the next user landing on this device (or the same
      // user re-signing in) doesn't inherit live QUIC connections
      // bound to the previous bearer.
      connectionManager.disconnectAll();
      setDevices([]);
      setActiveDevice(null);
      setConnectionStatus("disconnected");
      setUserDisconnected(false);
    }
  }, [token, refreshDevices]);

  // Reachability-driven auto-connect (applies after login / relaysReady).
  // PINGS every device once (yaver-level reachability) and connects to the
  // best REACHABLE one in priority order — explicit sticky pick (if reachable)
  // → primary → secondary → first reachable alphabetically. The old rule used
  // the stale heartbeat `online` flag, which kept selecting a box that was
  // "online" in Convex but unreachable from the phone, then hung on an
  // optimistic "Connecting". If devices exist but NONE respond, land in a
  // terminal "Can't connect" state instead of hanging. With no devices at all
  // the picker/empty-state handles the "no remote device added" case. One
  // attempt per nonce (bumped on device-set change + manual Retry) so a
  // failing connect can't loop. The user-disconnect flag always wins.
  useEffect(() => {
    if (!settingsReady || !token || !relaysReady || userDisconnected) return;
    // Already genuinely connected to the focused device → nothing to do.
    if (activeDevice && connectedDeviceIds.includes(activeDevice.id)) return;
    if (autoConnectInFlightRef.current) return;
    if (autoConnectAttemptedNonceRef.current === autoConnectNonce) return;
    const candidates = devices.filter((d) => !d.isGuest);
    if (candidates.length === 0) return; // no devices → empty-state UI handles it

    autoConnectAttemptedNonceRef.current = autoConnectNonce;
    autoConnectInFlightRef.current = true;
    let cancelled = false;
    (async () => {
      try {
        setConnectionStatus("connecting");
        const reachable = new Set<string>();
        await Promise.all(
          candidates.map(async (d) => {
            const probe = await probeMobileDeviceStatus(d, token, 3000).catch(() => null);
            if (probe?.reachable) reachable.add(d.id);
          }),
        );
        if (cancelled) return;
        const alpha = [...candidates]
          .sort((a, b) => a.name.localeCompare(b.name))
          .map((d) => d.id);
        const pickId = [userSelectedDeviceIdRef.current, primaryDeviceId, secondaryDeviceId, ...alpha].find(
          (id): id is string => !!id && reachable.has(id),
        );

        if (!pickId) {
          setConnectionStatus("error");
          setLastError(
            "Can't connect — no device responded. Make sure one is running `yaver serve` and reachable on your network or relay.",
          );
          return;
        }
        const target = devices.find((d) => d.id === pickId);
        if (!target || cancelled) return;
        const reason =
          pickId === userSelectedDeviceIdRef.current ? "sticky" :
          pickId === primaryDeviceId ? "primary" :
          pickId === secondaryDeviceId ? "secondary" : "reachable";
        console.log(`[DeviceContext] Auto-connecting (${reason}) to`, target.name);
        sendTelemetry(token, "auto-connect", `${reason}: ${target.name}`, JSON.stringify({
          reason,
          relayCount: quicClient.relayServerCount,
          deviceId: target.id.slice(0, 8),
          reachableCount: reachable.size,
        }));
        await selectDevice(target);
        // Seed primaryDeviceId on a multi-device account's first auto-connect.
        if (devices.length > 1 && primaryDeviceId === null) {
          setPrimaryDevice(target.id).catch((e) => {
            appLog("warn", `[DeviceContext] Auto-set primaryDevice failed: ${e}`);
          });
        }
      } finally {
        autoConnectInFlightRef.current = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [autoConnectNonce, devices, token, relaysReady, settingsReady, activeDevice?.id, connectedDeviceIds, userDisconnected, primaryDeviceId, secondaryDeviceId, selectDevice, setPrimaryDevice]);

  // Background "warm the pool" pass. After the focused auto-connect
  // above settles, this effect quietly opens additional connections
  // for every other online + authed + non-guest device the user has,
  // so the multi-target wizard and Tasks tab can route to siblings
  // without a cold-connect penalty. The user explicitly asked for
  // "at opening app try to connect both" — without this, only the
  // single focused box came up at boot.
  //
  // Implementation: walk devices each time they refresh, pick the
  // ones that look healthy AND aren't already pooled, and ensure
  // their per-device QuicClient is connected. Errors swallow
  // silently — a failed sibling shouldn't pollute lastError or take
  // the focused connection down with it. We don't change focus; the
  // existing focused-auto-connect logic owns that.
  useEffect(() => {
    if (!token || !relaysReady || userDisconnected) return;
    const candidates = devices.filter((d) =>
      d.online &&
      !d.needsAuth &&
      !d.isGuest &&
      !connectedDeviceIds.includes(d.id) &&
      !unreachableSet.has(d.id),
    );
    if (candidates.length === 0) return;
    let cancelled = false;
    (async () => {
      for (const device of candidates) {
        if (cancelled) return;
        try {
          // ensureConnected dedupes against a parallel user-driven
          // selectDevice — without it, the warm-up's connect and the
          // user's connect would both call QuicClient.connect() and
          // trample each other's primeTarget state, which surfaced as
          // "Couldn't switch · Could not reach this device" on the
          // wizard's switch path even when the box was actually live.
          await connectionManager.ensureConnected(device.id, {
            host: device.host,
            port: device.port,
            token,
            lanIps: device.lanIps,
            sessionTunnels: tunnelServersForDevice(device),
            connectionPreferences: device.connectionPreferences,
          });
        } catch {
          // Silent. The sibling stays unpooled; user can tap it
          // explicitly from Devices tab if they need it later.
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [devices, token, relaysReady, userDisconnected, connectedDeviceIds, unreachableSet]);

  // Trigger immediate reconnection on network change (WiFi↔cellular roaming,
  // Wi-Fi → Wi-Fi roam between APs (same SSID, new IP), VPN/Tailscale toggle).
  useEffect(() => {
    let lastType: string | null = null;
    let lastIp: string | null = null;
    const unsubscribe = NetInfo.addEventListener((state) => {
      const currentType = state.type; // "wifi", "cellular", "none", etc.
      // NetInfo.details has ipAddress on iOS/Android for wifi+cellular; falsy for "none"/"unknown".
      const currentIp =
        state.details && typeof (state.details as { ipAddress?: string }).ipAddress === "string"
          ? (state.details as { ipAddress: string }).ipAddress
          : null;

      if (state.isConnected && activeDevice) {
        // Type change (WiFi → cellular, cellular → WiFi) — full re-probe.
        if (lastType && lastType !== currentType) {
          console.log(`[DeviceContext] Network changed: ${lastType} → ${currentType}`);
          sendTelemetry(token, "network-change", `${lastType} → ${currentType}`);
          quicClient.fullReconnect();
        } else if (lastType && lastType === currentType && lastIp && currentIp && lastIp !== currentIp) {
          // Same type but IP changed — Wi-Fi roam, VPN toggle, DHCP renew.
          // Stale tunnel will hang on the old route; reprobe.
          console.log(`[DeviceContext] IP changed (${currentType}): ${lastIp} → ${currentIp}`);
          sendTelemetry(token, "network-ip-change", `${currentType} ${lastIp}→${currentIp}`);
          quicClient.fullReconnect();
        } else if (!lastType) {
          // First event after mount or reconnection — just probe to be safe
          quicClient.triggerReconnect();
        }
      }
      lastType = currentType;
      lastIp = currentIp;
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

  // Overlay sub-minute bus presence on top of the Convex-derived
  // online flag. Convex `lastHeartbeat` only refreshes every 5 min,
  // so a freshly-rung peer would otherwise show offline until the
  // next polling tick. The bus pings every 60 s (see
  // BUS_PRESENCE_STALE_MS) — when we have a recent ping we flip
  // `online` to true. A stale bus entry naturally stops contributing
  // because `(Date.now() - at) < BUS_PRESENCE_STALE_MS` becomes
  // false; the 30 s polling re-runs this memo with a fresh `devices`
  // ref so freshness drifts at most one polling interval before the
  // UI catches up.
  const displayDevices = useMemo<Device[]>(() => {
    if (Object.keys(busPresence).length === 0) return devices;
    const now = Date.now();
    let mutated = false;
    const next = devices.map((d) => {
      if (d.online) return d;
      const at = busPresence[d.id];
      if (at && now - at < BUS_PRESENCE_STALE_MS) {
        mutated = true;
        return { ...d, online: true };
      }
      return d;
    });
    return mutated ? next : devices;
  }, [devices, busPresence]);

  const value = useMemo<DeviceState>(
    () => ({
      devices: displayDevices,
      activeDevice,
      connectionStatus,
      isLoadingDevices,
      userDisconnected,
      lastError,
      agentAuthExpired,
      recoverDeviceAuth,
      pendingClaims,
      refreshPendingClaims,
      claimPendingDevice,
      selectDevice,
      disconnect,
      refreshDevices,
      detachDevice: handleDetachDevice,
      removeDevice: handleRemoveDevice,
      setDeviceAlias: handleSetDeviceAlias,
      unreachableDeviceIds: Array.from(unreachableSet),
      markDeviceUnreachable,
      manualAuthRequiredDeviceIds: Array.from(manualAuthRequiredSet),
      stopReconnectAndBounce,
      retryConnection,
      guestInvitations,
      acceptGuestInvitation,
      acceptGuestByCode,
      inviteGuest,
      primaryDeviceId,
      setPrimaryDevice,
      secondaryDeviceId,
      setSecondaryDevice,
      primaryRunnerByDevice,
      primaryModelByDevice,
      primaryModeByDevice,
      primaryProviderByDevice,
      multiTargetMode,
      setMultiTargetMode,
      setPrimaryRunnerForDevice,
      latestCliVersion,
      connectedDeviceIds,
      disconnectDevice,
    }),
    [displayDevices, activeDevice, connectionStatus, isLoadingDevices, userDisconnected, lastError, agentAuthExpired, recoverDeviceAuth, pendingClaims, refreshPendingClaims, claimPendingDevice, selectDevice, disconnect, refreshDevices, handleDetachDevice, handleRemoveDevice, handleSetDeviceAlias, unreachableSet, markDeviceUnreachable, manualAuthRequiredSet, stopReconnectAndBounce, guestInvitations, acceptGuestInvitation, acceptGuestByCode, inviteGuest, primaryDeviceId, setPrimaryDevice, secondaryDeviceId, setSecondaryDevice, primaryRunnerByDevice, primaryModelByDevice, primaryModeByDevice, primaryProviderByDevice, multiTargetMode, setMultiTargetMode, setPrimaryRunnerForDevice, latestCliVersion, connectedDeviceIds, disconnectDevice, retryConnection]
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
