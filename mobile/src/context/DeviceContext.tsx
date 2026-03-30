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
import { beaconListener } from "../lib/beacon";
import { CONVEX_SITE_URL } from "../lib/constants";

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
  /** true when device is discovered via LAN beacon (same network) */
  local?: boolean;
  /** stable hardware ID (P2P only, never sent to Convex) */
  hwid?: string;
}

type ConnectionStatus = "disconnected" | "connecting" | "connected" | "error";

interface DeviceState {
  devices: Device[];
  activeDevice: Device | null;
  connectionStatus: ConnectionStatus;
  isLoadingDevices: boolean;
  /** true when user explicitly disconnected (not a network failure) */
  userDisconnected: boolean;
  /** Last connection error message (null if no error) */
  lastError: string | null;
  selectDevice: (device: Device) => Promise<void>;
  disconnect: () => void;
  refreshDevices: () => Promise<void>;
  detachDevice: (device: Device) => Promise<void>;
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
            name: d.name,
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
          };
        });
        // Deduplicate by hardware ID (stable, survives IP/hostname changes)
        // Fall back to name-based dedup if hwid not available
        const seen = new Map<string, Device>();
        for (const d of mapped) {
          const key = d.hwid || d.name; // prefer hwid, fall back to name
          const existing = seen.get(key);
          if (!existing || d.lastSeen > existing.lastSeen) {
            seen.set(key, d);
          }
        }
        // Filter out detached devices
        const detached = await getDetachedDevices();
        const finalDevices = [...seen.values()].filter(d => {
          const key = d.hwid || d.name;
          return !detached.has(key);
        });
        setDevices(finalDevices);
      } else {
        appLog("warn", `/devices/list failed: ${devicesRes.status}`);
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
  }, []);

  const handleDetachDevice = useCallback(async (device: Device) => {
    const key = device.hwid || device.name;
    await addDetachedDevice(key);
    // If detaching the active device, disconnect first
    if (activeDevice?.id === device.id) {
      quicClient.disconnect();
      setActiveDevice(null);
      setConnectionStatus("disconnected");
    }
    setDevices((prev) => prev.filter((d) => (d.hwid || d.name) !== key));
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
      } else if (state === "connecting") {
        setConnectionStatus("connecting");
      } else if (state === "error") {
        const attempt = quicClient.reconnectAttempt;
        const gaveUp = attempt >= 15;
        if (gaveUp) {
          quicClient.disconnect();
          setConnectionStatus("disconnected");
          setLastError("Could not reach device after 15 attempts");
        } else {
          setConnectionStatus("error");
          setLastError(`Reconnecting (${attempt}/15)...`);
        }
      } else if (state === "disconnected") {
        // QUIC client fully disconnected (e.g., via disconnect() call)
        // Don't clear activeDevice here — that's handled by the disconnect() callback
      }
    });
    return () => unsub();
  }, [activeDevice]);

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

  const value = useMemo<DeviceState>(
    () => ({
      devices,
      activeDevice,
      connectionStatus,
      isLoadingDevices,
      userDisconnected,
      lastError,
      selectDevice,
      disconnect,
      refreshDevices,
      detachDevice: handleDetachDevice,
    }),
    [devices, activeDevice, connectionStatus, isLoadingDevices, userDisconnected, lastError, selectDevice, disconnect, refreshDevices, handleDetachDevice]
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
