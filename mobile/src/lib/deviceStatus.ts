import { Platform } from "react-native";
import { quicClient } from "./quic";

export type MobileDeviceStatusProbe = {
  reachable: boolean;
  bootstrap: boolean;
  authExpired: boolean;
  checkedAt: number;
  path?: "relay" | "direct";
  info?: Record<string, any> | null;
  error?: string;
};

export type MobileDeviceLifecycleState =
  | "offline"
  | "bootstrap"
  | "yaver-auth-expired"
  | "ready-to-connect"
  | "connected";

type DeviceLike = {
  id: string;
  host: string;
  port?: number;
  lanIps?: string[];
  online?: boolean;
  needsAuth?: boolean;
  peerState?: "online" | "stale" | "offline";
  lastTunnelEvent?: {
    online: boolean;
    at: number;
  };
};

function hasRecentLiveSignal(device: Pick<DeviceLike, "lastTunnelEvent">, maxAgeMs = 90_000): boolean {
  return Boolean(
    device.lastTunnelEvent &&
    device.lastTunnelEvent.online &&
    device.lastTunnelEvent.at > 0 &&
    (Date.now() - device.lastTunnelEvent.at) < maxAgeMs,
  );
}

function parseInfo(data: Record<string, any> | null | undefined) {
  const mode = String(data?.mode || "").trim().toLowerCase();
  return {
    bootstrap: data?.needsAuth === true || mode === "bootstrap",
    authExpired: data?.authExpired === true && !(data?.needsAuth === true || mode === "bootstrap"),
  };
}

async function fetchInfoAt(
  url: string,
  headers: Record<string, string>,
  timeoutMs: number,
): Promise<Record<string, any> | null> {
  try {
    const res = await fetch(`${url}/info`, {
      headers,
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) return null;
    return await res.json().catch(() => null);
  } catch {
    return null;
  }
}

export async function probeMobileDeviceStatus(
  device: Pick<DeviceLike, "id" | "host" | "port" | "lanIps">,
  token?: string | null,
  timeoutMs = 3500,
): Promise<MobileDeviceStatusProbe> {
  const checkedAt = Date.now();
  const port = device.port || 18080;
  let lastError = "No reachable transport";

  if (token && device.id) {
    for (const relay of quicClient.getRelayServers()) {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${token}`,
        "X-Client-Platform": Platform.OS,
      };
      if (relay.password) headers["X-Relay-Password"] = relay.password;
      const info = await fetchInfoAt(`${relay.httpUrl}/d/${device.id}`, headers, timeoutMs);
      if (info) {
        const parsed = parseInfo(info);
        return {
          reachable: true,
          bootstrap: parsed.bootstrap,
          authExpired: parsed.authExpired,
          checkedAt,
          path: "relay",
          info,
        };
      }
      lastError = `Relay ${relay.id || relay.httpUrl} unreachable`;
    }
  }

  const directTargets = Array.from(
    new Set([
      `http://${device.host}:${port}`,
      ...(device.lanIps || []).filter(Boolean).map((ip) => `http://${ip}:${port}`),
    ]),
  );
  for (const target of directTargets) {
    const info = await fetchInfoAt(target, {}, timeoutMs);
    if (info) {
      const parsed = parseInfo(info);
      return {
        reachable: true,
        bootstrap: parsed.bootstrap,
        authExpired: parsed.authExpired,
        checkedAt,
        path: "direct",
        info,
      };
    }
    lastError = `${target} unreachable`;
  }

  return {
    reachable: false,
    bootstrap: false,
    authExpired: false,
    checkedAt,
    error: lastError,
  };
}

export function deriveMobileDeviceLifecycleState(args: {
  device: Pick<DeviceLike, "online" | "needsAuth" | "peerState" | "lastTunnelEvent">;
  probe?: MobileDeviceStatusProbe | null;
  isConnected?: boolean;
  authExpired?: boolean;
  unreachable?: boolean;
}): MobileDeviceLifecycleState {
  const { device, probe, isConnected = false, authExpired = false, unreachable = false } = args;
  if (isConnected) return "connected";
  if (probe?.bootstrap) return "bootstrap";
  if (probe?.authExpired || authExpired) return "yaver-auth-expired";
  if (
    probe?.reachable ||
    device.online ||
    device.peerState === "online" ||
    device.peerState === "stale" ||
    hasRecentLiveSignal(device) ||
    unreachable
  ) {
    return "ready-to-connect";
  }
  return "offline";
}
