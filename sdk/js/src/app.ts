/**
 * YaverApp — the clean developer-API facade (@yaver/server).
 *
 * This is the BOUNDARY a consuming application uses. A consumer never touches
 * Yaver internals (Convex routes, relay passwords, the transport ladder, device
 * tokens). It holds one secret — the org's Yaver account token — and calls a
 * handful of high-level methods:
 *
 *     const app = new YaverApp({ accountToken });
 *     const devices = await app.listDevices();
 *     const status  = await app.status(deviceId);        // online / linked / runners
 *     const handle  = await app.sessionHandle(deviceId); // opaque bundle for the client
 *
 * The client (browser/mobile, @yaver/client) receives the opaque handle and
 * does `connect(handle)` → trigger tasks + stream output. All reachability
 * (direct / tunnel / relay P2P) lives inside the SDK.
 *
 * Generic: works for any on-prem AI utilization tool, not a specific app.
 */
import { YaverConvexClient, type DeviceCoords, DEFAULT_CONVEX_URL } from './discovery';
import { YaverBroker, type ConnectBundle } from './broker';
import { connect, type AgentStatus, type ConnectOptions } from './connect';

export interface YaverAppOptions {
  /** The org's Yaver account session token. SERVER-ONLY secret. */
  accountToken: string;
  convexUrl?: string;
}

/**
 * Opaque connection handle handed to a client. The client passes it (plus a
 * scoped token) to `connect()`. Consumers treat it as a black box.
 */
export interface SessionHandle extends ConnectBundle {
  /** Scoped bearer the agent accepts. Attach before sending to the client. */
  token?: string;
  convexUrl: string;
}

export class YaverApp {
  private readonly convex: YaverConvexClient;
  private readonly broker: YaverBroker;
  readonly convexUrl: string;

  constructor(opts: YaverAppOptions) {
    this.convexUrl = (opts.convexUrl ?? DEFAULT_CONVEX_URL).replace(/\/+$/, '');
    this.convex = new YaverConvexClient(opts.accountToken, this.convexUrl);
    this.broker = new YaverBroker({ accountToken: opts.accountToken, convexUrl: this.convexUrl });
  }

  /** Devices (agents) the org can reach, with presence. */
  listDevices(): Promise<DeviceCoords[]> {
    return this.convex.listDevices();
  }

  /** Build the opaque handle a client needs to connect. Attach `token` before sending. */
  async sessionHandle(deviceId: string, token?: string): Promise<SessionHandle> {
    const bundle = await this.broker.prepareSession(deviceId);
    return { ...bundle, token, convexUrl: this.convexUrl };
  }

  /**
   * High-level status. Presence (online) comes from Convex; deeper state
   * (account linked, runners ready) is read from the agent over the best
   * transport when a token is available. Never exposes internals.
   */
  async status(deviceId: string, token?: string): Promise<AppStatus> {
    const bundle = await this.broker.prepareSession(deviceId);
    const base: AppStatus = {
      deviceId: bundle.deviceId,
      online: bundle.online,
      reachable: false,
      accountLinked: false,
      runners: [],
      defaultRunner: null,
      ready: false,
    };
    if (!token || !bundle.online) return base;
    try {
      const opts: ConnectOptions = {
        deviceId: bundle.deviceId,
        token,
        device: bundle.device ?? undefined,
        relay: bundle.relay,
        relayServers: bundle.relayServers,
        tunnelUrl: bundle.tunnelUrl,
        forceRelay: bundle.forceRelay,
      };
      const session = await connect(opts);
      const s: AgentStatus = await session.status();
      return {
        ...base,
        reachable: s.reachable,
        accountLinked: s.accountLinked,
        runners: s.runners,
        defaultRunner: s.defaultRunner,
        ready: s.ready,
        transport: s.transport,
      };
    } catch {
      return base;
    }
  }
}

export interface AppStatus {
  deviceId: string;
  online: boolean;
  reachable: boolean;
  accountLinked: boolean;
  runners: AgentStatus['runners'];
  defaultRunner: string | null;
  ready: boolean;
  transport?: AgentStatus['transport'];
}
