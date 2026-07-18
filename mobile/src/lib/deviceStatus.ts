import { Platform } from "react-native";
import { quicClient } from "./quic";

export type MobileDeviceStatusProbe = {
  reachable: boolean;
  bootstrap: boolean;
  authExpired: boolean;
  codingReady: boolean;
  codingRunners: CodingRunnerProbe[];
  lifecycleState?: MobileDeviceLifecycleState | null;
  checkedAt: number;
  path?: "relay" | "direct";
  info?: Record<string, any> | null;
  error?: string;
  /**
   * Machine-readable form of `error`, so a caller can ACT on the reason instead
   * of matching prose.
   *
   * `relay-credentials-missing` is the recoverable one: relay servers are
   * configured but not one of them carries a password, which is the stale/absent
   * per-user credential. DeviceContext.repairRelay fixes exactly this, and until
   * this field existed the switch path could only tell the user to "sign in
   * again" — the self-heal keys off connectionStatus/lastError and never sees a
   * probe failure. Observed live: a mini that was up and reachable over its
   * tailnet reported "no transport answered" because every relay attempt was
   * password-less.
   */
  errorCode?: "relay-credentials-missing" | "no-transport" | "no-transport-configured";
};

export type CodingRunnerProbe = {
  id: string;
  name?: string;
  installed: boolean;
  ready: boolean;
  authConfigured: boolean;
  error?: string;
  warning?: string;
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

function normalizeRunner(row: any): CodingRunnerProbe | null {
  const id = String(row?.id || row?.runnerId || "").trim().toLowerCase();
  if (!id) return null;
  const installed = row?.installed === true;
  const error = typeof row?.error === "string" ? row.error : undefined;

  // FAIL CLOSED. This used to be `row?.authConfigured !== false`, which reads an
  // ABSENT field as authenticated — and the agent dropped the field entirely
  // when it was false (Go omits a false bool under `omitempty`). The two bugs
  // multiplied: a signed-out runner arrived as `{}` and rendered green.
  //
  // Only an explicit `true` counts as signed in. Old agents (< 1.99.300) that
  // genuinely omit the field now read as NOT ready, which is the safe direction
  // to be wrong in: it prompts a sign-in the user can act on, instead of
  // promising a runner that cannot run.
  const authConfigured = row?.authConfigured === true;
  return {
    id,
    name: typeof row?.name === "string" ? row.name : undefined,
    installed,
    ready: installed && row?.ready === true && authConfigured && !error,
    authConfigured,
    error,
    warning: typeof row?.warning === "string" ? row.warning : undefined,
  };
}

async function fetchCodingRunnersAt(
  url: string,
  headers: Record<string, string>,
  timeoutMs: number,
): Promise<CodingRunnerProbe[]> {
  try {
    const res = await fetch(`${url}/agent/runners`, {
      headers,
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) return [];
    const data = await res.json().catch(() => null);
    const rows = Array.isArray(data?.runners) ? data.runners : [];
    return rows.map(normalizeRunner).filter((r: CodingRunnerProbe | null): r is CodingRunnerProbe => !!r);
  } catch {
    return [];
  }
}

function codingReady(runners: CodingRunnerProbe[]): boolean {
  return runners.some((r) =>
    (r.id === "claude" || r.id === "claude-code" || r.id === "codex" || r.id === "opencode") &&
    r.ready,
  );
}

export async function probeMobileDeviceStatus(
  device: Pick<DeviceLike, "id" | "host" | "port" | "lanIps">,
  token?: string | null,
  timeoutMs = 3500,
): Promise<MobileDeviceStatusProbe> {
  const checkedAt = Date.now();
  const port = device.port || 18080;

  // Race ALL transports (every relay + every direct target) in parallel and
  // take the first that answers. The old code tried relay-first, serially — so
  // a box whose relay tunnel is registered-but-not-forwarding (heartbeating
  // "online" yet 502/timeout on the relay path) burned the full timeout on the
  // dead relay before ever trying its working direct LAN leg. Racing lets the
  // 40ms direct win regardless of a hung relay.
  type Attempt = { base: string; headers: Record<string, string>; path: "relay" | "direct" };
  const attempts: Attempt[] = [];
  let relayAttempts = 0;
  let passwordedRelayAttempts = 0;
  if (token && device.id) {
    for (const relay of quicClient.getRelayServers()) {
      relayAttempts += 1;
      const headers: Record<string, string> = {
        Authorization: `Bearer ${token}`,
        "X-Client-Platform": Platform.OS,
      };
      if (relay.password) {
        headers["X-Relay-Password"] = relay.password;
        passwordedRelayAttempts += 1;
      }
      attempts.push({ base: `${relay.httpUrl}/d/${device.id}`, headers, path: "relay" });
    }
  }
  const directHeaders: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
  for (const target of Array.from(
    new Set([
      `http://${device.host}:${port}`,
      // Probe the agent's real HTTP port (18080) too — a stale/mismatched
      // Convex quicPort shouldn't hide a reachable box.
      ...(device.host ? [`http://${device.host}:18080`] : []),
      ...(device.lanIps || []).filter(Boolean).flatMap((ip) => [`http://${ip}:${port}`, `http://${ip}:18080`]),
    ]),
  )) {
    attempts.push({ base: target, headers: directHeaders, path: "direct" });
  }

  const winner = attempts.length
    ? await Promise.any(
        attempts.map(async (a) => {
          const info = await fetchInfoAt(a.base, a.headers, timeoutMs);
          if (!info) throw new Error(`${a.path} unreachable`);
          return { a, info };
        }),
      ).catch(() => null)
    : null;

  if (winner) {
    const parsed = parseInfo(winner.info);
    const codingRunners = await fetchCodingRunnersAt(winner.a.base, winner.a.headers, timeoutMs);
    return {
      reachable: true,
      bootstrap: parsed.bootstrap,
      authExpired: parsed.authExpired,
      codingReady: codingReady(codingRunners),
      codingRunners,
      lifecycleState: parsed.lifecycleState,
      checkedAt,
      path: winner.a.path,
      info: winner.info,
    };
  }

  return {
    reachable: false,
    bootstrap: false,
    authExpired: false,
    codingReady: false,
    codingRunners: [],
    lifecycleState: null,
    checkedAt,
    error:
      relayAttempts > 0 && passwordedRelayAttempts === 0
        ? "No reachable transport. Sign in again to fetch relay credentials."
        : attempts.length
          ? "No reachable transport (tried relay + direct)"
          : "No transport configured",
    errorCode:
      relayAttempts > 0 && passwordedRelayAttempts === 0
        ? "relay-credentials-missing"
        : attempts.length
          ? "no-transport"
          : "no-transport-configured",
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
