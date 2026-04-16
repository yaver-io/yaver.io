import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Alert, Linking, Platform } from "react-native";
import Constants from "expo-constants";
import NetInfo from "@react-native-community/netinfo";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { router } from "expo-router";
import { quicClient, RelayServer, TunnelServer } from "../lib/quic";
import { useAuth } from "./AuthContext";
import { getUserSettings } from "../lib/auth";
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

// Heartbeat is sent every 2 minutes; consider "recently active" if within 5 min
const HEARTBEAT_STALE_MS = 5 * 60 * 1000;

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
  /** guest may use host-managed credentials without seeing raw secret */
  useHostApiKeys?: boolean;
  /** guest may bring their own credentials on top of host infra */
  allowGuestProvidedApiKeys?: boolean;
}

function deviceIdentityKey(device: Pick<Device, "id" | "hwid" | "name" | "isGuest" | "hostEmail" | "hostName">): string {
  if (device.hwid) return `hwid:${device.hwid}`;
  if (device.isGuest) {
    const hostScope = device.hostEmail || device.hostName || "guest";
    return `guest:${hostScope}:${device.id || device.name}`;
  }
  if (device.id) return `id:${device.id}`;
  return `name:${device.name}`;
}

type ConnectionStatus = "disconnected" | "connecting" | "connected" | "error";

interface GuestInvitation {
  /** Convex row id — present on records fetched from the backend. */
  _id?: string;
  hostUserId: string;
  hostName: string;
  hostEmail: string;
  createdAt: number;
  expiresAt: number;
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
  selectDevice: (device: Device) => Promise<void>;
  disconnect: () => void;
  refreshDevices: () => Promise<void>;
  detachDevice: (device: Device) => Promise<void>;
  /** Pending guest invitations from other users */
  guestInvitations: GuestInvitation[];
  /** Accept a guest invitation by email match */
  acceptGuestInvitation: (hostUserId: string) => Promise<void>;
  /** Accept a guest invitation by 6-char invite code (works with any OAuth email) */
  acceptGuestByCode: (code: string) => Promise<{ hostName: string; hostEmail: string }>;
  /** Invite someone as a guest to your machine */
  inviteGuest: (email: string) => Promise<{ inviteCode: string; guestRegistered: boolean }>;
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
  const hasLoadedOnce = useRef(false);

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
            needsAuth: d.needsAuth ?? false,
            isGuest: d.isGuest || false,
            hostName: d.hostName,
            hostEmail: d.hostEmail,
            accessScope: d.accessScope,
            priorityMode: d.priorityMode,
            useHostApiKeys: d.useHostApiKeys,
            allowGuestProvidedApiKeys: d.allowGuestProvidedApiKeys,
          };
        });
        // Deduplicate by stable device identity. Guest devices must include
        // host context so two different hosts with the same machine name
        // cannot collapse into one visible entry.
        const seen = new Map<string, Device>();
        for (const d of mapped) {
          const key = deviceIdentityKey(d);
          const existing = seen.get(key);
          if (!existing || d.lastSeen > existing.lastSeen) {
            seen.set(key, d);
          }
        }
        // Filter out detached devices
        const detached = await getDetachedDevices();
        const finalDevices = [...seen.values()].filter(d => {
          const key = deviceIdentityKey(d);
          return !detached.has(key);
        });
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
        // Race connect against a 10s timeout
        const connectPromise = quicClient.connect(device.host, device.port, token, device.id);
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

  // Fetch relay servers: local AsyncStorage > Convex user settings > Convex platform config
  // Extracted so it can be called on startup AND on reconnection (when relay list is empty).
  const fetchRelayServers = useCallback(async () => {
    try {
      // 1. Check for user-configured custom relays in local storage first
      const customRaw = await AsyncStorage.getItem(RELAYS_KEY);
      if (customRaw) {
        const customRelays: RelayServer[] = JSON.parse(customRaw);
        if (customRelays.length > 0) {
          quicClient.setRelayServers(customRelays);
          console.log("[DeviceContext] Using", customRelays.length, "custom relay server(s)");
          return;
        }
      }

      // 2. No local relays — check Convex user settings (account-level relay config)
      if (token) {
        try {
          const settings = await getUserSettings(token);
          if (settings.relayUrl) {
            const accountRelay: RelayServer = {
              id: "account",
              quicAddr: "",
              httpUrl: settings.relayUrl,
              region: "account",
              priority: 1,
              password: settings.relayPassword,
            };
            quicClient.setRelayServers([accountRelay]);
            // Persist to AsyncStorage so it works offline and on next launch
            await AsyncStorage.setItem(RELAYS_KEY, JSON.stringify([accountRelay]));
            await AsyncStorage.setItem(SYNC_KEY, "true");
            console.log("[DeviceContext] Loaded relay from Convex user settings:", settings.relayUrl);
            return;
          }
        } catch {
          // Best-effort — fall through to platform config
        }
      }

      // 3. No account-level relay — fall back to Convex platform config
      const res = await fetch(`${CONVEX_SITE_URL}/config`);
      if (res.ok) {
        const data = await res.json();
        const servers: RelayServer[] = data.relayServers || [];
        quicClient.setRelayServers(servers);
        console.log("[DeviceContext] Loaded", servers.length, "relay server(s) from Convex");
      }
    } catch {
      sendTelemetry(token, "relays-failed", "Could not fetch relay config");
    }
  }, [token]);

  // Initial relay fetch on mount
  const relaysFetched = useRef(false);
  useEffect(() => {
    if (relaysFetched.current) return;
    relaysFetched.current = true;
    fetchRelayServers().finally(() => setRelaysReady(true));
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
      setDevices((prev) =>
        prev.map((d) => {
          if (d.id.startsWith(discovered.deviceId)) {
            return { ...d, host: discovered.ip, port: discovered.port, online: true, local: true, hwid: discovered.hwid || d.hwid };
          }
          return d;
        })
      );
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
        autoPairedRef.current.add(dev.deviceId);
        const targetUrl = `http://${dev.ip}:${dev.port}`;
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
            const res = await submitEncryptedPair(targetUrl, token, pubKey);
            if (res.ok) {
              appLog("info", `Encrypted auto-pair: ${dev.name || dev.deviceId} at ${dev.ip}`);
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
            setTimeout(() => refreshDevices(), 3000);
          }
        } catch {
          autoPairedRef.current.delete(dev.deviceId);
        }
      }
    }, 3000);
    return () => clearInterval(iv);
  }, [token, user?.id, devices, refreshDevices]);

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
        (d) => (!d.online || d.needsAuth === true) && !d.isGuest && d.publicKey && !probed.has(d.id) && !autoPairedRef.current.has(d.id)
      );
      for (const dev of offlineDevices) {
        probed.add(dev.id);
        const relayUrl = `${relays[0].httpUrl}/d/${dev.id}`;
        try {
          const infoRes = await fetch(`${relayUrl}/info`, { signal: AbortSignal.timeout(5000) });
          if (!infoRes.ok) continue;
          const info = await infoRes.json();
          if (!info.needsAuth) continue;

          autoPairedRef.current.add(dev.id);
          const res = await submitEncryptedPair(relayUrl, token, dev.publicKey!);
          if (res.ok) {
            appLog("info", `Relay encrypted auto-pair: ${dev.name} via ${relays[0].httpUrl}`);
            setTimeout(() => refreshDevices(), 3000);
          } else {
            autoPairedRef.current.delete(dev.id);
          }
        } catch {
          // Device not reachable via relay — normal for truly offline devices
        }
      }
    }, 15000);
    return () => clearInterval(iv);
  }, [token, user?.id, devices, refreshDevices]);

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
    const iv = setInterval(async () => {
      if (autoPairedRef.current.has(activeDevice.id)) return;
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
          const ok = await submitEncryptedPair(targetUrl, token, activeDevice.publicKey);
          if (ok.ok) {
            appLog("info", `Direct encrypted auto-pair: ${activeDevice.name} at ${activeDevice.host}`);
            setTimeout(() => refreshDevices(), 3000);
            return;
          }
        }
        // Fallback: passkey pair — need to fetch passkey from bootstrap /info response
        const passkey = info.bootstrapPasskey || info.passkey;
        if (!passkey) {
          autoPairedRef.current.delete(activeDevice.id);
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
          setTimeout(() => refreshDevices(), 3000);
        } else {
          autoPairedRef.current.delete(activeDevice.id);
        }
      } catch {
        // Network error — will retry next tick
      }
    }, 5000);
    return () => clearInterval(iv);
  }, [token, user?.id, activeDevice?.id, activeDevice?.host, activeDevice?.port, activeDevice?.publicKey, activeDevice?.name, refreshDevices]);

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
        const recovery = await quicClient.recoverAgent(undefined, "pair");
        if (!recovery?.ok || !recovery.pairCode) {
          appLog("warn", `Host-token recovery did not open a pair session for ${activeDevice.name}: ${recovery?.error || "unknown error"}`);
          return;
        }
        const pairRes = await submitPair({
          code: recovery.pairCode,
          targetUrl: recovery.targetUrl || quicClient.baseUrl,
          token,
          userId: user.id,
        });
        if (!pairRes.ok) {
          appLog("warn", `Auth recovery pair submit failed for ${activeDevice.name}: ${pairRes.error || "unknown error"}`);
          return;
        }
        quicClient.agentAuthExpired = false;
        setAgentAuthExpired(false);
        appLog("info", `Recovered expired agent session for ${activeDevice.name} from mobile`);
        setTimeout(() => refreshDevices(), 2000);
      } finally {
        if (!cancelled) {
          setTimeout(() => {
            recoveringAuthRef.current.delete(activeDevice.id);
          }, 5000);
        }
      }
    };

    tryRecover().catch(() => {
      recoveringAuthRef.current.delete(activeDevice.id);
    });

    return () => {
      cancelled = true;
    };
  }, [token, user?.id, activeDevice, agentAuthExpired, refreshDevices]);

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

  // Auto-connect: single online device → connect immediately (unless user disconnected)
  // Wait for relaysReady so the QUIC client has relay servers before attempting connection
  useEffect(() => {
    if (!token || !relaysReady || activeDevice || connectionStatus === "connecting" || userDisconnected) return;

    const recentDevices = devices.filter((d) => d.online);

    if (recentDevices.length === 1) {
      console.log("[DeviceContext] Auto-connecting to single online device:", recentDevices[0].name);
      sendTelemetry(token, "auto-connect", `Single device: ${recentDevices[0].name}`, JSON.stringify({
        relayCount: quicClient.relayServerCount, deviceId: recentDevices[0].id.slice(0, 8),
      }));
      selectDevice(recentDevices[0]);
    }
    // Multiple devices → don't auto-connect, let UI prompt user
  }, [devices, token, relaysReady, activeDevice, connectionStatus, userDisconnected, selectDevice]);

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

  const acceptGuestInvitation = useCallback(async (hostUserId: string) => {
    if (!token) return;
    await apiAcceptInvitation(token, hostUserId);
    await refreshDevices();
  }, [token, refreshDevices]);

  const acceptGuestByCode = useCallback(async (code: string) => {
    if (!token) throw new Error("Not signed in");
    const result = await apiAcceptByCode(token, code);
    await refreshDevices();
    return result;
  }, [token, refreshDevices]);

  const inviteGuest = useCallback(async (email: string) => {
    if (!token) throw new Error("Not signed in");
    return await apiInviteGuest(token, email);
  }, [token]);

  const value = useMemo<DeviceState>(
    () => ({
      devices,
      activeDevice,
      connectionStatus,
      isLoadingDevices,
      userDisconnected,
      lastError,
      agentAuthExpired,
      selectDevice,
      disconnect,
      refreshDevices,
      detachDevice: handleDetachDevice,
      guestInvitations,
      acceptGuestInvitation,
      acceptGuestByCode,
      inviteGuest,
    }),
    [devices, activeDevice, connectionStatus, isLoadingDevices, userDisconnected, lastError, agentAuthExpired, selectDevice, disconnect, refreshDevices, handleDetachDevice, guestInvitations, acceptGuestInvitation, acceptGuestByCode, inviteGuest]
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
