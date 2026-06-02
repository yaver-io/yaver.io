/**
 * broker — the SERVER side (@yaver/server). Runs where the org's Yaver account
 * secret can live safely (your app's backend). It NEVER ships the account token
 * to a browser/app; instead it assembles the connection bundle a client needs
 * (device coordinates + relay endpoint/password + tunnel) and lets the caller
 * attach a scoped client token. This is the generic embedding pattern for any
 * on-prem AI tool that wants its users to reach a Yaver agent.
 *
 *   app client  --ask-->  app server (YaverBroker)  --discover-->  Yaver Convex
 *               <--bundle + scoped token--             (account secret stays here)
 *   app client  --connect()-->  agent  (direct / tunnel / relay P2P)
 */
import { YaverConvexClient, type DeviceCoords, type RelayServer, type YaverSettings, DEFAULT_CONVEX_URL } from './discovery';

export interface ConnectBundle {
  deviceId: string;
  device: DeviceCoords | null;
  relay: { url: string; password: string } | null;
  relayServers: RelayServer[];
  tunnelUrl?: string;
  forceRelay?: boolean;
  /** Convenience: is the device currently online? */
  online: boolean;
}

export interface YaverBrokerOptions {
  /** The org (e.g. Simkab) Yaver account session token. Server-only secret. */
  accountToken: string;
  convexUrl?: string;
}

export class YaverBroker {
  readonly convex: YaverConvexClient;
  constructor(opts: YaverBrokerOptions) {
    this.convex = new YaverConvexClient(opts.accountToken, opts.convexUrl ?? DEFAULT_CONVEX_URL);
  }

  listDevices(): Promise<DeviceCoords[]> {
    return this.convex.listDevices();
  }

  getSettings(): Promise<YaverSettings> {
    return this.convex.getSettings();
  }

  /**
   * Assemble everything a client needs to reach `deviceId` over the best
   * transport. Attach a scoped client token before handing this to the client
   * (the bundle deliberately carries NO token).
   */
  async prepareSession(deviceId: string): Promise<ConnectBundle> {
    const [devices, settings, config] = await Promise.all([
      this.convex.listDevices().catch(() => [] as DeviceCoords[]),
      this.convex.getSettings().catch(() => ({} as YaverSettings)),
      this.convex.getConfig().catch(() => ({ relayServers: [] as RelayServer[] })),
    ]);
    const device = devices.find((d) => d.deviceId === deviceId || (deviceId.length >= 8 && d.deviceId?.startsWith(deviceId))) ?? null;
    const relay = settings.relayUrl && settings.relayPassword
      ? { url: settings.relayUrl, password: settings.relayPassword }
      : null;
    return {
      deviceId: device?.deviceId ?? deviceId,
      device,
      relay,
      relayServers: config.relayServers ?? [],
      tunnelUrl: settings.tunnelUrl,
      forceRelay: settings.forceRelay,
      online: Boolean(device?.isOnline),
    };
  }
}
