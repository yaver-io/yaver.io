import { Platform } from "react-native";
import { quicClient } from "./quic";

export type MobileDeviceStatusProbe = {
  reachable: boolean;
  bootstrap: boolean;
  authExpired: boolean;
  lifecycleState?: MobileDeviceLifecycleState | null;
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

/** Human "last seen …" label from a heartbeat/last-signal epoch (ms).
 * Used by device pickers to show DOWN machines honestly instead of
 * implying they're reachable. Mirrors the relative-time formatting used
 * by the Devices list and DeviceDetailsModal, with a "last seen" prefix
 * so a device row reads "Down · last seen 12m ago". */
export function lastSeenLabel(epochMs?: number): string {
  if (!epochMs || epochMs <= 0) return "never connected";
  const sec = Math.floor(Math.max(0, Date.now() - epochMs) / 1000);
  if (sec < 60) return "last seen just now";
  const m = Math.floor(sec / 60);
  if (m < 60) return `last seen ${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `last seen ${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 7) return `last seen ${d}d ago`;
  const dt = new Date(epochMs);
  return `last seen ${dt.toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;
}

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
  const lifecycleState = String(
    data?.lifecycle?.state || data?.lifecycleState || "",
  ).trim().toLowerCase();
  const mode = String(data?.mode || "").trim().toLowerCase();
  return {
    lifecycleState:
      lifecycleState === "bootstrap" ||
      lifecycleState === "yaver-auth-expired" ||
      lifecycleState === "ready-to-connect"
        ? (lifecycleState as MobileDeviceLifecycleState)
        : null,
    bootstrap:
      lifecycleState === "bootstrap" ||
      data?.needsAuth === true ||
      mode === "bootstrap",
    authExpired:
      lifecycleState === "yaver-auth-expired" ||
      (data?.authExpired === true && !(data?.needsAuth === true || mode === "bootstrap")),
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
          lifecycleState: parsed.lifecycleState,
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
        lifecycleState: parsed.lifecycleState,
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
    lifecycleState: null,
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
  const { device, probe, isConnected = false, authExpired = false } = args;
  // Auth state is independent of transport. Even when this mobile reached
  // the agent (isConnected=true), the agent's own yaver session can be
  // expired — manifests as 401 on subsequent requests (agentAuthExpired)
  // or as needsAuth on the convex row. Surface the auth state first so
  // the card flips to "Re-auth & Connect" instead of silently claiming
  // "connected" while the user's tasks 401.
  if (probe?.bootstrap) return "bootstrap";
  if (probe?.authExpired || authExpired) return "yaver-auth-expired";
  if (device.needsAuth) return "yaver-auth-expired";
  if (isConnected) return "connected";
  if (probe?.lifecycleState) return probe.lifecycleState;
  // "ready-to-connect" must mean we have a *positive, recent* signal that the
  // box can actually be reached: a live probe, a fresh Convex heartbeat
  // (device.online ≤ HEARTBEAT_STALE_MS), live P2P presence, or a relay tunnel
  // seen up in the last 90s. A `stale` peerState or a failed reachability probe
  // (`unreachable`) are NOT live signals — they previously let a down box like
  // magara render "READY · reachable" while a connect attempt timed out. Let
  // those fall through to "offline" so the card shows the honest
  // "No recent heartbeat" copy instead.
  if (
    probe?.reachable ||
    device.online ||
    device.peerState === "online" ||
    hasRecentLiveSignal(device)
  ) {
    return "ready-to-connect";
  }
  return "offline";
}
