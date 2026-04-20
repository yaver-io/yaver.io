// AsyncStorage is an optional peer dep — gracefully degrade if missing.
// Resolved lazily on each call so unit-test mocks of
// `@react-native-async-storage/async-storage` are picked up even when
// Discovery.ts is imported before the mock is applied.
type AsyncStorageLike = {
  getItem: (key: string) => Promise<string | null>;
  setItem: (key: string, value: string) => Promise<void>;
  removeItem: (key: string) => Promise<void>;
};
function getAsyncStorage(): AsyncStorageLike | null {
  try {
    const mod = require('@react-native-async-storage/async-storage');
    const candidate = mod?.default ?? mod;
    if (candidate && typeof candidate.getItem === 'function') {
      return candidate as AsyncStorageLike;
    }
    return null;
  } catch {
    return null;
  }
}

import type { RemoteDevice } from './auth';
import {
  collapseRemoteDevices,
  pickTargetDevice,
  HEARTBEAT_STALE_MS,
} from './deviceDedup';

const STORAGE_KEY = 'yaver_feedback_agent';
const DEFAULT_PORT = 18080;
const PROBE_TIMEOUT_MS = 2500;
const RELAY_PROBE_TIMEOUT_MS = 6000;

export interface DiscoveryResult {
  url: string;
  hostname: string;
  version: string;
  latency: number;
}

// LAN fallback sweep used ONLY when Convex lookup fails AND the stored
// cache probe fails. Keep tight — covers 192.168.1/0.x and 10.0.0/1.x
// with a handful of common host suffixes. The primary path is always
// Convex.
const LAN_SUBNETS = ['192.168.1', '192.168.0', '10.0.0', '10.0.1'];
const LAN_HOST_SUFFIXES = [1, 2, 50, 100, 101, 200];

/**
 * Device discovery for finding Yaver agents.
 *
 * **Convex is the primary source of truth.** The user's Convex account
 * has the freshest IP / port for each registered agent, updated every
 * 2 minutes via heartbeat. The SDK should therefore:
 *   1. On every `discover()` call, re-query Convex for the latest IP
 *      (no local cache shortcut when `convexUrl` + `authToken` are
 *      available).
 *   2. Dedup the returned list (Convex can carry stale rows after
 *      re-pair) and pick the freshest online machine.
 *   3. Probe the machine's `quicHost:httpPort` directly.
 *   4. If the direct probe fails (different LAN / roaming), route
 *      through the configured relay.
 *   5. Store the successful URL only AFTER the probe confirms it's
 *      reachable. Stored cache is used only as a last-chance shortcut
 *      when Convex itself is unreachable.
 *
 * Compared to the previous implementation this removes the "trust
 * stored URL first" shortcut that caused the SDK to keep trying a dead
 * cached IP long after the Mac's IP rotated.
 */
export class YaverDiscovery {
  static async discover(options?: {
    convexUrl?: string;
    authToken?: string;
    preferredDeviceId?: string;
  }): Promise<DiscoveryResult | null> {
    // Strategy 1: Convex — always tried first when credentials are
    // available. No cache shortcut.
    if (options?.convexUrl && options?.authToken) {
      const result = await YaverDiscovery.discoverFromConvex(
        options.convexUrl,
        options.authToken,
        options.preferredDeviceId,
      );
      if (result) {
        await YaverDiscovery.store(result);
        return result;
      }
    }

    // Strategy 2: Stored URL. Only used as a fallback when Convex was
    // unreachable. A successful probe here means either the mobile is
    // offline or Convex is — we'll trust the stored IP.
    const stored = await YaverDiscovery.getStored();
    if (stored) {
      const result = await YaverDiscovery.probe(stored.url);
      if (result) return result;
      await YaverDiscovery.clear();
    }

    // Strategy 3: LAN fallback. Small sweep of common subnets. This is
    // only hit when the user has no Convex session (device-local mode)
    // or both Convex + cache lookups failed.
    const candidates: string[] = [];
    for (const subnet of LAN_SUBNETS) {
      for (const suffix of LAN_HOST_SUFFIXES) {
        candidates.push(`http://${subnet}.${suffix}:${DEFAULT_PORT}`);
      }
    }
    const results = await Promise.allSettled(
      candidates.map((url) => YaverDiscovery.probe(url)),
    );
    for (const r of results) {
      if (r.status === 'fulfilled' && r.value) {
        await YaverDiscovery.store(r.value);
        return r.value;
      }
    }
    return null;
  }

  /**
   * Re-query Convex ignoring any cached URL. Intended for the call
   * site right after a probe/network failure — it's the "the IP
   * probably changed, ask the source of truth again" path.
   */
  static async refreshFromConvex(options: {
    convexUrl: string;
    authToken: string;
    preferredDeviceId?: string;
  }): Promise<DiscoveryResult | null> {
    await YaverDiscovery.clear();
    const result = await YaverDiscovery.discoverFromConvex(
      options.convexUrl,
      options.authToken,
      options.preferredDeviceId,
    );
    if (result) await YaverDiscovery.store(result);
    return result;
  }

  /**
   * Fetch the agent URL from Convex. Dedups rows, prefers fresh ones,
   * falls back to relay if the direct LAN IP isn't reachable.
   */
  static async discoverFromConvex(
    convexUrl: string,
    authToken: string,
    preferredDeviceId?: string,
  ): Promise<DiscoveryResult | null> {
    const base = convexUrl.replace(/\/$/, '');
    try {
      // Try cloud machines first (CPU/GPU managed machines). These are
      // long-lived with stable IPs so the direct probe is cheap.
      const machinesRes = await fetch(`${base}/machines`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (machinesRes.ok) {
        const { machines } = await machinesRes.json();
        const activeMachine = (machines ?? []).find(
          (m: { status: string; serverIp?: string }) =>
            m.status === 'active' && m.serverIp,
        );
        if (activeMachine?.serverIp) {
          const url = `http://${activeMachine.serverIp}:${DEFAULT_PORT}`;
          const probed = await YaverDiscovery.probe(url);
          if (probed) return probed;
        }
      }

      // Fall back to personal devices registered in Convex.
      const devicesRes = await fetch(`${base}/devices/list`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (!devicesRes.ok) return null;
      const data = await devicesRes.json();
      const rawList = Array.isArray(data?.devices) ? data.devices : data;
      if (!Array.isArray(rawList) || rawList.length === 0) return null;

      // Normalise Convex fields → RemoteDevice shape so dedup works the
      // same way `listReachableDevices` does it.
      const normalised: RemoteDevice[] = rawList.map((d: any) => ({
        deviceId: d.deviceId ?? d.id,
        name: d.name ?? '',
        platform: d.platform ?? d.os ?? '',
        isOnline: !!d.isOnline,
        needsAuth: !!d.needsAuth,
        runnerDown: !!d.runnerDown,
        lastHeartbeat: d.lastHeartbeat ?? 0,
        isGuest: !!d.isGuest,
        hostName: d.hostName,
        hostEmail: d.hostEmail,
        accessScope: d.accessScope ?? 'owner',
        quicHost: d.quicHost ?? d.host ?? '',
        quicPort: d.quicPort ?? 0,
        httpPort: d.httpPort ?? d.quicPort,
        publicKey: d.publicKey,
        hwid: d.hardwareId ?? d.hwid,
        localIps: Array.isArray(d.localIps)
          ? d.localIps
          : Array.isArray(d.lanIps)
            ? d.lanIps
            : undefined,
      }));

      const deduped = collapseRemoteDevices(normalised);
      const target = pickTargetDevice(deduped, preferredDeviceId);
      if (!target) return null;

      // Build the same candidate set the Yaver mobile app races on: the
      // primary `quicHost` plus every LAN IP reported in the latest
      // heartbeat (`localIps`). Multi-homed hosts commonly advertise
      // en0 + utun (tailscale) + docker0 etc.; probing all of them in
      // parallel makes the SDK "just work" on the same Wi-Fi without
      // depending on which NIC the user's router DHCP'd them from.
      const port = target.httpPort ?? target.quicPort ?? DEFAULT_PORT;
      const ipSet = new Set<string>();
      if (target.quicHost) ipSet.add(target.quicHost);
      for (const ip of target.localIps ?? []) {
        if (ip) ipSet.add(ip);
      }
      const candidates = Array.from(ipSet).map(
        (ip) => `http://${ip}:${port}`,
      );

      if (candidates.length > 0) {
        const direct = await YaverDiscovery.raceProbe(candidates);
        if (direct) return direct;
      }

      // Warn if the chosen target looks stale — informative only; we
      // still fell through to the relay path below.
      const stale =
        target.lastHeartbeat &&
        Date.now() - target.lastHeartbeat > HEARTBEAT_STALE_MS;
      void stale;

      // Direct probes all failed — route through relay.
      const relayResult = await YaverDiscovery.discoverViaRelay(
        base,
        authToken,
        target.deviceId,
      );
      if (relayResult) return relayResult;

      return null;
    } catch {
      return null;
    }
  }

  /**
   * Race `/health` probes across N URLs in parallel. First 200 wins;
   * everything else is abandoned. Mirrors the mobile app's
   * `raceDirectCandidates` pattern — the single most reliable thing it
   * does on same-LAN.
   */
  static async raceProbe(urls: string[]): Promise<DiscoveryResult | null> {
    if (!urls || urls.length === 0) return null;
    const attempts = urls.map((url) =>
      YaverDiscovery.probe(url).then((r) => {
        if (!r) throw new Error('no-200');
        return r;
      }),
    );
    try {
      // `Promise.any` isn't on every RN runtime yet (older Hermes).
      // Hand-roll the same behaviour so we don't need a polyfill.
      return await new Promise<DiscoveryResult | null>((resolve) => {
        let remaining = attempts.length;
        let settled = false;
        attempts.forEach((p) => {
          p.then((r) => {
            if (settled) return;
            settled = true;
            resolve(r);
          }).catch(() => {
            remaining -= 1;
            if (remaining <= 0 && !settled) {
              settled = true;
              resolve(null);
            }
          });
        });
      });
    } catch {
      return null;
    }
  }

  /**
   * Discover agent via relay HTTP proxy. Uses the user's configured
   * relay first (from `/auth/validate`), then the platform relay list.
   */
  static async discoverViaRelay(
    convexUrl: string,
    authToken: string,
    deviceId: string,
  ): Promise<DiscoveryResult | null> {
    try {
      const settingsRes = await fetch(`${convexUrl}/auth/validate`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      let relayUrl: string | undefined;
      let relayPassword: string | undefined;

      if (settingsRes.ok) {
        const settingsData = await settingsRes.json();
        relayUrl = settingsData.relayUrl;
        relayPassword = settingsData.relayPassword;
      }

      if (!relayUrl) {
        const configRes = await fetch(`${convexUrl}/platform-config?key=relay_servers`);
        if (configRes.ok) {
          const configData = await configRes.json();
          const servers =
            typeof configData.value === 'string'
              ? JSON.parse(configData.value)
              : configData.value;
          if (Array.isArray(servers) && servers.length > 0) {
            const relay = servers.find((s: { httpUrl?: string }) => s.httpUrl);
            if (relay) relayUrl = relay.httpUrl;
          }
        }
      }
      if (!relayUrl) return null;

      const relayBase = `${relayUrl.replace(/\/$/, '')}/d/${deviceId}`;
      return YaverDiscovery.probeWithHeaders(relayBase, {
        'X-Relay-Password': relayPassword || '',
      });
    } catch {
      return null;
    }
  }

  static async probeWithHeaders(
    url: string,
    headers: Record<string, string>,
  ): Promise<DiscoveryResult | null> {
    const base = url.replace(/\/$/, '');
    const start = Date.now();
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), RELAY_PROBE_TIMEOUT_MS);
      const response = await fetch(`${base}/health`, {
        method: 'GET',
        headers,
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
      if (!response.ok) return null;
      const latency = Date.now() - start;
      let hostname = 'Unknown';
      let version = 'unknown';
      try {
        const data = await response.json();
        hostname = data.hostname ?? data.name ?? 'Unknown';
        version = data.version ?? 'unknown';
      } catch {
        // /health may return plain text
      }
      return { url: base, hostname, version, latency };
    } catch {
      return null;
    }
  }

  /** Probe a specific URL for a running Yaver agent (2.5 s timeout). */
  static async probe(url: string): Promise<DiscoveryResult | null> {
    const base = url.replace(/\/$/, '');
    const start = Date.now();
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
      const response = await fetch(`${base}/health`, {
        method: 'GET',
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
      if (!response.ok) return null;
      const latency = Date.now() - start;
      let hostname = 'Unknown';
      let version = 'unknown';
      try {
        const data = await response.json();
        hostname = data.hostname ?? data.name ?? 'Unknown';
        version = data.version ?? 'unknown';
      } catch {
        // /health may return plain text
      }
      return { url: base, hostname, version, latency };
    } catch {
      return null;
    }
  }

  static async connect(url: string): Promise<DiscoveryResult | null> {
    const result = await YaverDiscovery.probe(url);
    if (result) await YaverDiscovery.store(result);
    return result;
  }

  static async getStored(): Promise<{ url: string; hostname: string } | null> {
    const storage = getAsyncStorage();
    if (!storage) return null;
    try {
      const raw = await storage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed.url === 'string') {
        return { url: parsed.url, hostname: parsed.hostname ?? 'Unknown' };
      }
      return null;
    } catch {
      return null;
    }
  }

  static async store(result: DiscoveryResult): Promise<void> {
    const storage = getAsyncStorage();
    if (!storage) return;
    try {
      await storage.setItem(
        STORAGE_KEY,
        JSON.stringify({ url: result.url, hostname: result.hostname }),
      );
    } catch {
      // Storage failure is non-fatal
    }
  }

  static async clear(): Promise<void> {
    const storage = getAsyncStorage();
    if (!storage) return;
    try {
      await storage.removeItem(STORAGE_KEY);
    } catch {
      // Storage failure is non-fatal
    }
  }
}
