import {
  DEFAULT_CONVEX_SITE_URL,
  listReachableDevices,
  type RemoteDevice,
} from './auth';
import type { DiscoveryResult } from './types';

const STORAGE_KEY = 'yaver_feedback_agent';
const DEFAULT_PORT = 18080;
const TIMEOUT_MS = 2000;
const RELAY_TIMEOUT_MS = 6000;

// Common local network prefixes to scan in the no-token fallback path.
const LOCAL_PREFIXES = ['192.168.1', '192.168.0', '10.0.0', '10.0.1', '172.16.0'];

/**
 * A single reachability candidate — either a direct URL (LAN / Tailscale /
 * tunnel / public IP) or a relayed URL with required headers. The discovery
 * layer races an array of these and returns the first one whose /health
 * responds successfully.
 */
interface Candidate {
  url: string;
  headers?: Record<string, string>;
  timeoutMs?: number;
  /** For diagnostics only — never surfaced to callers. */
  label?: string;
}

interface RelayEntry {
  httpUrl?: string;
  password?: string;
  priority?: number;
}

interface RelayInfo {
  servers: RelayEntry[];
  userRelayPassword?: string;
}

/**
 * YaverDiscovery finds the Yaver agent behind a given deviceId by racing
 * every transport Convex advertises — LAN IPs, Tailscale, Cloudflare /
 * custom HTTP tunnels, and the platform relay — in parallel. Whichever
 * path responds first wins; the rest are discarded. Transports are not
 * mutually exclusive: a user can be on WiFi with Tailscale running and
 * a relay server registered, and discovery will pick the fastest one
 * that actually works from this browser.
 */
export class YaverDiscovery {
  /**
   * Entry point used by YaverFeedback.init / ensureAgentConnection. If
   * an auth token is available we resolve the machine via Convex (which
   * knows every advertised transport for the device); otherwise fall
   * back to a no-auth localhost + LAN scan — only useful for local dev.
   */
  static async discover(options?: {
    convexUrl?: string;
    authToken?: string;
    preferredDeviceId?: string;
  }): Promise<DiscoveryResult | null> {
    if (options?.authToken) {
      const result = await YaverDiscovery.discoverFromConvex(
        options.convexUrl ?? DEFAULT_CONVEX_SITE_URL,
        options.authToken,
        options.preferredDeviceId,
      );
      if (result) {
        YaverDiscovery.store(result);
        return result;
      }
    }

    // ─ Unauthenticated fallback (local-dev only) ─────────────────────
    const stored = YaverDiscovery.getStored();
    if (stored) {
      const result = await YaverDiscovery.probe(stored.url);
      if (result) return result;
    }

    const localhost = await YaverDiscovery.probe(`http://localhost:${DEFAULT_PORT}`);
    if (localhost) {
      YaverDiscovery.store(localhost);
      return localhost;
    }

    const scanCandidates: Candidate[] = [];
    for (const prefix of LOCAL_PREFIXES) {
      for (const host of [1, 2, 100, 101, 50, 200]) {
        scanCandidates.push({ url: `http://${prefix}.${host}:${DEFAULT_PORT}` });
      }
    }
    const scanHit = await YaverDiscovery.raceCandidates(scanCandidates);
    if (scanHit) {
      YaverDiscovery.store(scanHit);
      return scanHit;
    }
    return null;
  }

  /**
   * Core authenticated path. Collects every candidate transport the
   * device advertises — LAN IPs, quicHost (may be public, Tailscale, or
   * LAN depending on how the agent registered), explicit tunnelUrl /
   * publicUrl, Tailscale-ish 100.64.0.0/10 hosts, and every relay in
   * platform config (with the user's password override on top) — then
   * races them all concurrently.
   */
  static async discoverFromConvex(
    convexUrl: string,
    authToken: string,
    preferredDeviceId?: string,
  ): Promise<DiscoveryResult | null> {
    const devices = await listReachableDevices(authToken);
    const all = [...devices.owned, ...devices.shared];
    if (all.length === 0) return null;

    const target =
      all.find((device) => device.deviceId === preferredDeviceId) ??
      all.find((device) => device.isOnline && !device.needsAuth && !device.runnerDown) ??
      all[0];
    if (!target) return null;

    const candidates: Candidate[] = [];

    // ─ Direct transports (no auth headers needed on /health) ─────────
    const port = readNumberField(target, 'httpPort') ?? target.quicPort ?? DEFAULT_PORT;

    const addDirectHost = (host: string | undefined, label: string) => {
      if (!host) return;
      candidates.push({ url: `http://${host}:${port}`, label });
    };
    const addDirectUrl = (url: string | undefined, label: string) => {
      if (!url) return;
      // Trim trailing slash — probe appends /health.
      candidates.push({ url: url.replace(/\/$/, ''), label });
    };

    // quicHost can be any of: RFC1918, Tailscale 100.x, link-local,
    // public IP, or even a DNS name. We do NOT pre-filter — the race
    // lets the browser's actual routing decide which one succeeds.
    addDirectHost(target.quicHost, 'quic-host');

    // Arrays of LAN IPs the agent told Convex about on registration.
    const lanIps =
      readStringArrayField(target, 'localIps') ??
      readStringArrayField(target, 'lanIps') ??
      [];
    for (const ip of lanIps) addDirectHost(ip, 'lan-ip');

    // Tailscale addresses if the agent tagged itself with one.
    const tsIps =
      readStringArrayField(target, 'tailscaleIps') ??
      readStringArrayField(target, 'tsIps') ??
      [];
    for (const ip of tsIps) addDirectHost(ip, 'tailscale');
    const tsIp =
      readStringField(target, 'tailscaleIp') ??
      readStringField(target, 'tsIp');
    if (tsIp) addDirectHost(tsIp, 'tailscale');

    // Cloudflare / named / custom HTTP tunnel. May include scheme (https://)
    // so we pass as URL, not host+port.
    addDirectUrl(readStringField(target, 'tunnelUrl'), 'tunnel');
    addDirectUrl(readStringField(target, 'publicUrl'), 'public-url');
    addDirectUrl(readStringField(target, 'cfTunnelUrl'), 'cf-tunnel');

    // Publicly-reachable IP if the agent declared one.
    addDirectHost(readStringField(target, 'publicIp'), 'public-ip');

    // ─ Relay transports (need Bearer + optional X-Relay-Password) ────
    const relayInfo = await YaverDiscovery.fetchRelayInfo(convexUrl, authToken);
    for (const relay of relayInfo.servers) {
      if (!relay.httpUrl) continue;
      const password = relay.password ?? relayInfo.userRelayPassword;
      const headers: Record<string, string> = {
        Authorization: `Bearer ${authToken}`,
      };
      if (password) headers['X-Relay-Password'] = password;
      candidates.push({
        url: `${relay.httpUrl.replace(/\/$/, '')}/d/${target.deviceId}`,
        headers,
        timeoutMs: RELAY_TIMEOUT_MS,
        label: `relay:${relay.httpUrl}`,
      });
    }

    if (candidates.length === 0) return null;

    const winner = await YaverDiscovery.raceCandidates(candidates);
    if (winner) YaverDiscovery.store(winner);
    return winner;
  }

  /**
   * Public probe helper (no headers). Kept for backwards compatibility
   * with the unauthenticated fallback path and the manual connect flow.
   */
  static async probe(url: string): Promise<DiscoveryResult | null> {
    return YaverDiscovery.probeCandidate({ url, timeoutMs: TIMEOUT_MS });
  }

  /** Legacy helper kept for callers outside discovery. */
  static async probeWithHeaders(
    url: string,
    headers: Record<string, string>,
  ): Promise<DiscoveryResult | null> {
    return YaverDiscovery.probeCandidate({ url, headers, timeoutMs: RELAY_TIMEOUT_MS });
  }

  /**
   * Manual connect — used by host code that already knows the agent URL.
   */
  static async connect(url: string): Promise<DiscoveryResult | null> {
    const result = await YaverDiscovery.probe(url);
    if (result) YaverDiscovery.store(result);
    return result;
  }

  /** Persist the last working URL to localStorage (24 h TTL on read). */
  static store(result: DiscoveryResult): void {
    try {
      localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          url: result.url,
          hostname: result.hostname,
          timestamp: Date.now(),
        }),
      );
    } catch {
      // localStorage not available
    }
  }

  static getStored(): { url: string; hostname: string } | null {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const data = JSON.parse(raw);
      if (Date.now() - data.timestamp > 24 * 60 * 60 * 1000) return null;
      return data;
    } catch {
      return null;
    }
  }

  static clear(): void {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore
    }
  }

  /**
   * Race every candidate concurrently. Resolves with the first one whose
   * /health responded successfully, OR null if all failed. A slow path
   * (relay over a mediocre connection) can still win if no faster path
   * exists — there's no short-circuit timer at this level; each
   * candidate's own `timeoutMs` controls its individual budget.
   */
  private static async raceCandidates(
    candidates: Candidate[],
  ): Promise<DiscoveryResult | null> {
    if (candidates.length === 0) return null;
    return new Promise<DiscoveryResult | null>((resolve) => {
      let remaining = candidates.length;
      let done = false;
      for (const candidate of candidates) {
        YaverDiscovery.probeCandidate(candidate)
          .then((result) => {
            if (done) return;
            if (result) {
              done = true;
              resolve(result);
              return;
            }
            remaining--;
            if (remaining === 0 && !done) {
              done = true;
              resolve(null);
            }
          })
          .catch(() => {
            if (done) return;
            remaining--;
            if (remaining === 0 && !done) {
              done = true;
              resolve(null);
            }
          });
      }
    });
  }

  private static async probeCandidate(
    candidate: Candidate,
  ): Promise<DiscoveryResult | null> {
    const timeoutMs = candidate.timeoutMs ?? TIMEOUT_MS;
    try {
      const start = Date.now();
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      const resp = await fetch(`${candidate.url}/health`, {
        headers: candidate.headers,
        signal: controller.signal,
      });
      clearTimeout(timer);
      if (!resp.ok) return null;
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      return {
        url: candidate.url,
        hostname: typeof data.hostname === 'string' ? data.hostname : 'unknown',
        version: typeof data.version === 'string' ? data.version : 'unknown',
        latency: Date.now() - start,
      };
    } catch {
      return null;
    }
  }

  /**
   * Fetch the platform-default relay server list plus the signed-in
   * user's relayUrl / relayPassword overrides. Mirrors the web
   * dashboard's resolution:
   *   - GET /config (public)   → platform relay servers
   *   - GET /settings (Bearer) → user's custom password + optional
   *                              self-hosted relay URL (added first).
   */
  private static async fetchRelayInfo(
    convexUrl: string,
    authToken: string,
  ): Promise<RelayInfo> {
    const base = convexUrl.replace(/\/$/, '');
    const servers: RelayEntry[] = [];

    const [configRes, settingsRes] = await Promise.allSettled([
      fetch(`${base}/config`),
      fetch(`${base}/settings`, {
        headers: { Authorization: `Bearer ${authToken}` },
      }),
    ]);

    if (configRes.status === 'fulfilled' && configRes.value.ok) {
      try {
        const data = await configRes.value.json();
        for (const r of (data.relayServers ?? []) as RelayEntry[]) {
          if (r.httpUrl) servers.push(r);
        }
      } catch {
        /* bad JSON */
      }
    }

    let userRelayPassword: string | undefined;
    if (settingsRes.status === 'fulfilled' && settingsRes.value.ok) {
      try {
        const data = await settingsRes.value.json();
        userRelayPassword =
          (data?.settings?.relayPassword as string | undefined) ??
          (data?.relayPassword as string | undefined);
        const userRelayUrl =
          (data?.settings?.relayUrl as string | undefined) ??
          (data?.relayUrl as string | undefined);
        if (userRelayUrl) {
          servers.unshift({
            httpUrl: userRelayUrl,
            password: userRelayPassword,
            priority: -1,
          });
        }
      } catch {
        /* bad JSON */
      }
    }

    servers.sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
    return { servers, userRelayPassword };
  }
}

function readNumberField(device: RemoteDevice, key: string): number | undefined {
  const value = (device as unknown as Record<string, unknown>)[key];
  return typeof value === 'number' ? value : undefined;
}

function readStringField(device: RemoteDevice, key: string): string | undefined {
  const value = (device as unknown as Record<string, unknown>)[key];
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function readStringArrayField(device: RemoteDevice, key: string): string[] | undefined {
  const value = (device as unknown as Record<string, unknown>)[key];
  return Array.isArray(value)
    ? value.filter((entry): entry is string => typeof entry === 'string')
    : undefined;
}
