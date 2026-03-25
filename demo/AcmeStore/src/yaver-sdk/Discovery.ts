import AsyncStorage from '@react-native-async-storage/async-storage';

const STORAGE_KEY = 'yaver_feedback_agent';
const DEFAULT_PORT = 18080;
const TIMEOUT_MS = 2000;

export interface DiscoveryResult {
  url: string;
  hostname: string;
  version: string;
  latency: number;
}

// Common LAN subnets and host suffixes to scan
const SUBNETS = ['192.168.1', '192.168.0', '10.0.0', '10.0.1'];
const HOST_SUFFIXES = [1, 2, 50, 100, 101, 200];

/**
 * Device discovery for finding Yaver agents on the local network or via Convex.
 *
 * Three discovery strategies (tried in order):
 * 1. **Convex cloud** — fetch agent IP from Convex `/devices/list` (for cloud machines)
 * 2. **Stored connection** — try cached URL from last successful connection
 * 3. **LAN scan** — probe common LAN IPs via `/health` endpoint
 */
export class YaverDiscovery {
  /**
   * Discover an agent. Tries Convex cloud first (if configured),
   * then stored connection, then LAN scan.
   */
  static async discover(options?: {
    convexUrl?: string;
    authToken?: string;
    preferredDeviceId?: string;
  }): Promise<DiscoveryResult | null> {
    // Strategy 1: Convex cloud discovery (for cloud machines)
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

    // Strategy 2: Try stored connection
    const stored = await YaverDiscovery.getStored();
    if (stored) {
      const result = await YaverDiscovery.probe(stored.url);
      if (result) {
        return result;
      }
      await YaverDiscovery.clear();
    }

    // Strategy 3: Scan common LAN IPs in parallel
    const candidates: string[] = [];
    for (const subnet of SUBNETS) {
      for (const suffix of HOST_SUFFIXES) {
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
   * Fetch the agent URL from Convex device list or cloud machines.
   * No hardcoded IPs needed — Convex knows where the agent is.
   */
  static async discoverFromConvex(
    convexUrl: string,
    authToken: string,
    preferredDeviceId?: string,
  ): Promise<DiscoveryResult | null> {
    const base = convexUrl.replace(/\/$/, '');

    try {
      // Try cloud machines first (CPU/GPU managed machines)
      const machinesRes = await fetch(`${base}/machines`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });

      if (machinesRes.ok) {
        const { machines } = await machinesRes.json();
        const activeMachine = (machines ?? []).find(
          (m: { status: string; serverIp?: string }) => m.status === 'active' && m.serverIp,
        );
        if (activeMachine?.serverIp) {
          const url = `http://${activeMachine.serverIp}:${DEFAULT_PORT}`;
          const probed = await YaverDiscovery.probe(url);
          if (probed) return probed;
        }
      }

      // Fall back to device list (personal machines registered with Convex)
      const devicesRes = await fetch(`${base}/devices/list`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });

      if (!devicesRes.ok) return null;
      const data = await devicesRes.json();
      const devices = data.devices ?? data ?? [];

      // Find preferred device or first online one
      const target = preferredDeviceId
        ? devices.find((d: { deviceId: string }) => d.deviceId === preferredDeviceId)
        : devices.find((d: { isOnline: boolean }) => d.isOnline);

      if (!target?.quicHost) return null;

      const port = target.httpPort ?? DEFAULT_PORT;
      const url = `http://${target.quicHost}:${port}`;
      return await YaverDiscovery.probe(url);
    } catch {
      return null;
    }
  }

  /**
   * Probe a specific URL for a running Yaver agent.
   * Hits the `/health` endpoint with a 2s timeout.
   */
  static async probe(url: string): Promise<DiscoveryResult | null> {
    const base = url.replace(/\/$/, '');
    const start = Date.now();

    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), TIMEOUT_MS);

      const response = await fetch(`${base}/health`, {
        method: 'GET',
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      if (!response.ok) {
        return null;
      }

      const latency = Date.now() - start;

      let hostname = 'Unknown';
      let version = 'unknown';

      try {
        const data = await response.json();
        hostname = data.hostname ?? data.name ?? 'Unknown';
        version = data.version ?? 'unknown';
      } catch {
        // Health endpoint might return plain text — that's fine
      }

      return { url: base, hostname, version, latency };
    } catch {
      return null;
    }
  }

  /**
   * Manually connect to a specific agent URL.
   * Probes the URL and stores the connection if successful.
   */
  static async connect(url: string): Promise<DiscoveryResult | null> {
    const result = await YaverDiscovery.probe(url);
    if (result) {
      await YaverDiscovery.store(result);
    }
    return result;
  }

  /** Get the cached agent connection from AsyncStorage. */
  static async getStored(): Promise<{ url: string; hostname: string } | null> {
    try {
      const raw = await AsyncStorage.getItem(STORAGE_KEY);
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

  /** Store a successful discovery result in AsyncStorage. */
  static async store(result: DiscoveryResult): Promise<void> {
    try {
      await AsyncStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({ url: result.url, hostname: result.hostname }),
      );
    } catch {
      // Storage failure is non-fatal
    }
  }

  /** Clear the stored agent connection. */
  static async clear(): Promise<void> {
    try {
      await AsyncStorage.removeItem(STORAGE_KEY);
    } catch {
      // Storage failure is non-fatal
    }
  }
}
