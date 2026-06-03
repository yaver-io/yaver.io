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
import {
  YaverPolicyClient,
  type CompanyAIOptions,
  type CompanyAIOptionsResponse,
  type ResolveRequest,
  type ResolvedSession,
} from './policy';
import {
  composeEntitlements,
  entitlementFromResolved,
  type Entitlement,
  type EffectiveEntitlement,
} from './acl';

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
  /**
   * Policy the client must render within. The client treats these as the
   * menu of allowed choices — it never widens them. The agent enforces them
   * authoritatively from the token scope.
   */
  resolved?: ResolvedSession;
  allowedRunners?: string[];
  runner?: string;
  model?: string;
  /** Effective entitlement after composing every ACL layer (company + user/guest/host-share). */
  effective?: EffectiveEntitlement;
}

export class YaverApp {
  private readonly convex: YaverConvexClient;
  private readonly broker: YaverBroker;
  private readonly policy: YaverPolicyClient;
  readonly convexUrl: string;

  constructor(opts: YaverAppOptions) {
    this.convexUrl = (opts.convexUrl ?? DEFAULT_CONVEX_URL).replace(/\/+$/, '');
    this.convex = new YaverConvexClient(opts.accountToken, this.convexUrl);
    this.broker = new YaverBroker({ accountToken: opts.accountToken, convexUrl: this.convexUrl });
    this.policy = new YaverPolicyClient(opts.accountToken, this.convexUrl);
  }

  /** Devices (agents) the org can reach, with presence. */
  listDevices(): Promise<DeviceCoords[]> {
    return this.convex.listDevices();
  }

  // ── Policy + runtime resolution (the generic control plane) ──────────

  /** Read a team's AI policy (safe defaults when unconfigured). */
  getPolicy(teamId: string): Promise<CompanyAIOptionsResponse> {
    return this.policy.getOptions(teamId);
  }

  /** Write a team's AI policy. Server enforces admin/owner role. */
  setPolicy(teamId: string, options: CompanyAIOptions): Promise<{ ok: boolean; id?: string; error?: string }> {
    return this.policy.setOptions(teamId, options);
  }

  /**
   * Resolve a concrete runtime (device + runner + model + provider + approvals)
   * for a unit of work. This is the shared contract every surface (web, mobile,
   * desktop, MCP) should call before starting a job. Returns no secrets.
   */
  resolve(req: ResolveRequest): Promise<ResolvedSession> {
    return this.policy.resolve(req);
  }

  /**
   * Mint a scoped, short-lived client token from the account — so clients never
   * receive the account secret. Hand the result to `sessionHandle`/the client.
   */
  mintClientToken(opts?: { label?: string; scopes?: string[]; ttlMs?: number }): Promise<{ token: string; expiresAt?: number }> {
    return this.convex.mintSdkToken({ label: opts?.label, scopes: opts?.scopes, expiresInMs: opts?.ttlMs });
  }

  /**
   * Build the opaque handle a client needs to connect. Attach `token` before
   * sending. Pass a `resolved` session to bake the allowed runner/model/policy
   * into the handle so the client renders only what policy permits.
   */
  async sessionHandle(
    deviceId: string,
    token?: string,
    resolved?: ResolvedSession,
    effective?: EffectiveEntitlement,
  ): Promise<SessionHandle> {
    const bundle = await this.broker.prepareSession(deviceId);
    const allowedRunners = effective?.allowedRunners ?? resolved?.runner.allowedRunners;
    return {
      ...bundle,
      token,
      convexUrl: this.convexUrl,
      resolved,
      effective,
      allowedRunners,
      runner: resolved?.runner.id,
      model: resolved?.runner.model,
    };
  }

  /**
   * One call: resolve company policy for a unit of work, COMPOSE it with any
   * other ACL layers the caller already has (the user's own prefs, a guest
   * grant, a host-share policy — pass them via `opts.entitlements`), mint a
   * scoped client token carrying the EFFECTIVE allowed-runner scope, and return
   * a ready handle.
   *
   * Composition is jointly inclusive: company policy never overrides the user's
   * own ACL, and an absent layer never forces anything (see acl.ts). The token
   * and handle carry the intersection, so the client cannot reach a runner that
   * ANY applicable layer disallows.
   */
  async resolvedHandle(
    req: ResolveRequest,
    opts?: { label?: string; ttlMs?: number; entitlements?: Entitlement[] },
  ): Promise<SessionHandle> {
    const resolved = await this.resolve(req);
    const deviceId = resolved.runtime.deviceId ?? req.requestedDeviceId;
    if (!deviceId) throw new Error('resolvedHandle: no runtime device bound for this team/work-kind');
    const effective = composeEntitlements([
      entitlementFromResolved(resolved),
      ...(opts?.entitlements ?? []),
    ]);
    const allowedRunners = effective.allowedRunners ?? resolved.runner.allowedRunners;
    const scopes = [
      `runners:${allowedRunners.join(',') || resolved.runner.id}`,
      `workKind:${resolved.workKind}`,
    ];
    const { token } = await this.mintClientToken({ label: opts?.label ?? `${req.source ?? 'api'}:${req.workKind}`, scopes, ttlMs: opts?.ttlMs });
    return this.sessionHandle(deviceId, token, resolved, effective);
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
