/**
 * Discovery — the Convex broker layer. Resolves "where is my agent and how do I
 * reach it" the way Yaver's own web/mobile apps do:
 *   GET /devices/list  -> device coordinates (localIps, publicEndpoints, online)
 *   GET /config        -> relay servers
 *   GET /settings      -> per-user relayUrl / relayPassword / tunnelUrl
 *
 * Works in browser, React Native, and Node. Pure fetch, no deps.
 */

export const DEFAULT_CONVEX_URL = 'https://perceptive-minnow-557.eu-west-1.convex.site';

export interface DeviceCoords {
  deviceId: string;
  name?: string;
  quicHost?: string;
  quicPort?: number;
  localIps?: string[];
  publicEndpoints?: string[];
  isOnline?: boolean;
  needsAuth?: boolean;
  connectionPreferences?: Array<{ kind: string; active?: boolean; preferred?: boolean }>;
}

export interface RelayServer {
  id?: string;
  httpUrl: string;
  quicAddr?: string;
  priority?: number;
}

export interface YaverSettings {
  forceRelay?: boolean;
  relayUrl?: string;
  relayPassword?: string;
  tunnelUrl?: string;
  primaryDeviceId?: string;
}

export class YaverConvexClient {
  convexUrl: string;
  token: string;

  /** token = a Yaver session bearer for the owning account. */
  constructor(token: string, convexUrl: string = DEFAULT_CONVEX_URL) {
    this.token = token;
    this.convexUrl = convexUrl.replace(/\/+$/, '');
  }

  /** All devices visible to this account, with reach coordinates + presence. */
  async listDevices(): Promise<DeviceCoords[]> {
    const r = await this.get<{ devices?: DeviceCoords[] } | DeviceCoords[]>('/devices/list');
    return Array.isArray(r) ? r : r?.devices ?? [];
  }

  /** Public relay catalogue (no per-user secret). */
  async getConfig(): Promise<{ relayServers: RelayServer[] }> {
    const r = await this.getPublic<{ relayServers?: RelayServer[] }>('/config');
    return { relayServers: r?.relayServers ?? [] };
  }

  /** Per-user relay endpoint + password + tunnel URL. */
  async getSettings(): Promise<YaverSettings> {
    const r = await this.get<Record<string, unknown>>('/settings');
    const inner = r && typeof r === 'object' && 'settings' in r ? (r.settings as YaverSettings) : (r as YaverSettings);
    return inner ?? {};
  }

  private async get<T>(path: string): Promise<T> {
    const res = await fetch(`${this.convexUrl}${path}`, {
      headers: { Authorization: `Bearer ${this.token}` },
    });
    if (!res.ok) throw new Error(`Convex ${path} -> HTTP ${res.status}`);
    return res.json();
  }

  private async getPublic<T>(path: string): Promise<T> {
    const res = await fetch(`${this.convexUrl}${path}`);
    if (!res.ok) throw new Error(`Convex ${path} -> HTTP ${res.status}`);
    return res.json();
  }
}
