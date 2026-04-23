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

// Common local network prefixes to scan
const LOCAL_PREFIXES = ['192.168.1', '192.168.0', '10.0.0', '10.0.1', '172.16.0'];

/**
 * YaverDiscovery finds Yaver agents on the local network.
 * Used by the feedback SDK to connect to the dev machine without manual config.
 */
export class YaverDiscovery {
  /**
   * Try to discover a Yaver agent on the local network.
   * Checks stored URL first, then scans common local IPs.
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

    // 1. Check stored connection
    const stored = YaverDiscovery.getStored();
    if (stored) {
      const result = await YaverDiscovery.probe(stored.url);
      if (result) return result;
    }

    // 2. Try localhost (agent on same machine)
    const localhost = await YaverDiscovery.probe(`http://localhost:${DEFAULT_PORT}`);
    if (localhost) {
      YaverDiscovery.store(localhost);
      return localhost;
    }

    // 3. Scan common local IPs (gateway .1 and common dev machine IPs)
    const candidates: string[] = [];
    for (const prefix of LOCAL_PREFIXES) {
      for (const host of [1, 2, 100, 101, 50, 200]) {
        candidates.push(`http://${prefix}.${host}:${DEFAULT_PORT}`);
      }
    }

    // Probe in parallel with timeout
    const results = await Promise.allSettled(
      candidates.map((url) => YaverDiscovery.probe(url))
    );

    for (const r of results) {
      if (r.status === 'fulfilled' && r.value) {
        YaverDiscovery.store(r.value);
        return r.value;
      }
    }

    return null;
  }

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

    const port = readNumberField(target, 'httpPort') ?? target.quicPort ?? DEFAULT_PORT;
    const candidates = new Set<string>();
    if (target.quicHost) candidates.add(`http://${target.quicHost}:${port}`);
    const localIps = readStringArrayField(target, 'localIps') ?? readStringArrayField(target, 'lanIps');
    for (const ip of localIps ?? []) {
      candidates.add(`http://${ip}:${port}`);
    }

    const direct = await YaverDiscovery.probeCandidates(Array.from(candidates));
    if (direct) return direct;

    return YaverDiscovery.discoverViaRelay(convexUrl, authToken, target.deviceId);
  }

  /**
   * Probe a specific URL to check if a Yaver agent is running there.
   */
  static async probe(url: string): Promise<DiscoveryResult | null> {
    try {
      const start = Date.now();
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), TIMEOUT_MS);

      const resp = await fetch(`${url}/health`, { signal: controller.signal });
      clearTimeout(timeout);

      if (!resp.ok) return null;

      const data = await resp.json();
      const latency = Date.now() - start;

      return {
        url,
        hostname: data.hostname || 'unknown',
        version: data.version || 'unknown',
        latency,
      };
    } catch {
      return null;
    }
  }

  static async probeWithHeaders(
    url: string,
    headers: Record<string, string>,
  ): Promise<DiscoveryResult | null> {
    try {
      const start = Date.now();
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), RELAY_TIMEOUT_MS);
      const resp = await fetch(`${url}/health`, { headers, signal: controller.signal });
      clearTimeout(timeout);
      if (!resp.ok) return null;
      const data = await resp.json();
      return {
        url,
        hostname: data.hostname || 'unknown',
        version: data.version || 'unknown',
        latency: Date.now() - start,
      };
    } catch {
      return null;
    }
  }

  /**
   * Manually connect to a known agent URL.
   */
  static async connect(url: string): Promise<DiscoveryResult | null> {
    const result = await YaverDiscovery.probe(url);
    if (result) {
      YaverDiscovery.store(result);
    }
    return result;
  }

  /** Store last known agent connection in localStorage. */
  static store(result: DiscoveryResult): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        url: result.url,
        hostname: result.hostname,
        timestamp: Date.now(),
      }));
    } catch {
      // localStorage not available
    }
  }

  /** Get stored agent connection. */
  static getStored(): { url: string; hostname: string } | null {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const data = JSON.parse(raw);
      // Expire after 24 hours
      if (Date.now() - data.timestamp > 24 * 60 * 60 * 1000) return null;
      return data;
    } catch {
      return null;
    }
  }

  /** Clear stored connection. */
  static clear(): void {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore
    }
  }

  private static async probeCandidates(urls: string[]): Promise<DiscoveryResult | null> {
    if (urls.length === 0) return null;
    const results = await Promise.allSettled(urls.map((url) => YaverDiscovery.probe(url)));
    for (const result of results) {
      if (result.status === 'fulfilled' && result.value) {
        return result.value;
      }
    }
    return null;
  }

  private static async discoverViaRelay(
    convexUrl: string,
    authToken: string,
    deviceId: string,
  ): Promise<DiscoveryResult | null> {
    try {
      const base = convexUrl.replace(/\/$/, '');
      let relayUrl: string | undefined;
      let relayPassword: string | undefined;

      const validate = await fetch(`${base}/auth/validate`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (validate.ok) {
        const data = await validate.json();
        relayUrl = data.relayUrl;
        relayPassword = data.relayPassword;
      }

      if (!relayUrl) {
        const config = await fetch(`${base}/platform-config?key=relay_servers`);
        if (config.ok) {
          const data = await config.json();
          const servers = typeof data.value === 'string' ? JSON.parse(data.value) : data.value;
          if (Array.isArray(servers)) {
            const relay = servers.find((candidate: { httpUrl?: string }) => candidate.httpUrl);
            relayUrl = relay?.httpUrl;
          }
        }
      }

      if (!relayUrl) return null;
      const headers: Record<string, string> = {};
      if (relayPassword) headers['X-Relay-Password'] = relayPassword;
      const relayBase = `${relayUrl.replace(/\/$/, '')}/d/${deviceId}`;
      return YaverDiscovery.probeWithHeaders(relayBase, headers);
    } catch {
      return null;
    }
  }
}

function readNumberField(device: RemoteDevice, key: string): number | undefined {
  const value = (device as unknown as Record<string, unknown>)[key];
  return typeof value === 'number' ? value : undefined;
}

function readStringArrayField(device: RemoteDevice, key: string): string[] | undefined {
  const value = (device as unknown as Record<string, unknown>)[key];
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : undefined;
}
