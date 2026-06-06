/**
 * fleet.ts — the yaver Fleet lib: drive a *set* of remote machines from code.
 *
 * Where YaverClient/connect talk to ONE agent, Fleet makes the whole fleet a
 * first-class object: select machines by tag/platform/online, then fan exec
 * (and, in later layers, agents / file-sync / verified-actions) across them
 * with results streamed back as one merged async iterator.
 *
 * It is a thin composition of pieces that already exist:
 *   - selection      → POST {convexUrl}/devices/select (the P2 selector query)
 *   - per-machine     → buildCandidates/pickTransport (the connect.ts ladder:
 *     transport          direct-LAN → tunnel → relay, health-raced)
 *   - exec            → the agent's /exec endpoints, called over the winning
 *                        transport (so relay's X-Relay-Password header rides along)
 *
 * @example
 * ```ts
 * import { Fleet } from 'yaver-sdk';
 * const fleet = await Fleet.connect({ token, relay });
 * const gpu = await fleet.select({ tags: ['gpu'], online: true });
 * for await (const { machine, stream, text } of gpu.exec('nvidia-smi -L')) {
 *   process.stdout.write(`[${machine.alias ?? machine.deviceId}] ${text}`);
 * }
 * ```
 */

import { DEFAULT_CONVEX_URL, type DeviceCoords } from './discovery';
import { buildCandidates, pickTransport, type Transport } from './connect';

export interface FleetConnectOptions {
  /** Yaver bearer the agents + Convex accept (the user's session token). */
  token: string;
  /** Convex site URL (defaults to the public deployment). */
  convexUrl?: string;
  /** Relay endpoint + shared password, for machines with no direct/tunnel path. */
  relay?: { url: string; password: string } | null;
  /** Agent HTTP port for direct-LAN probes (default 18080). */
  directPort?: number;
  /** Per-candidate health-probe timeout (ms). */
  probeTimeoutMs?: number;
}

/** A machine as returned by the selector — compact, privacy-safe. */
export interface MachineInfo {
  deviceId: string;
  name: string;
  alias: string | null;
  platform: string;
  tags: string[];
  online: boolean;
  quicHost: string;
  quicPort: number;
  localIps: string[];
  publicEndpoints: string[];
}

export interface SelectFilter {
  tags?: string[];
  /** Tag semantics: 'all' (default) = carry every tag; 'any' = at least one. */
  match?: 'all' | 'any';
  platform?: string;
  online?: boolean;
}

/** One line of streamed output, tagged with its source machine. */
export interface ExecLine {
  machine: MachineInfo;
  stream: 'stdout' | 'stderr';
  text: string;
}

/** Terminal result of an exec on one machine. */
export interface ExecResult {
  machine: MachineInfo;
  code: number;
  stdout: string;
  stderr: string;
  error?: string;
}

export interface ExecOpts {
  workDir?: string;
  timeout?: number;
  env?: Record<string, string>;
  /** Output poll interval (ms). */
  pollIntervalMs?: number;
}

interface ExecSessionView {
  status: string; // running | completed | failed | killed
  stdout: string;
  stderr: string;
  exitCode?: number;
}

/** Authed fetch over a resolved transport (bearer + transport headers e.g. relay pw). */
async function transportFetch(
  transport: Transport,
  token: string,
  path: string,
  init: RequestInit = {},
  json = false,
): Promise<Response> {
  return fetch(`${transport.baseURL}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(json ? { 'Content-Type': 'application/json' } : {}),
      ...transport.headers,
      ...(init.headers as Record<string, string> | undefined),
    },
  });
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * One machine in the fleet. Resolves a transport lazily (and caches the
 * winner) so a Selection of N machines only probes the ones it actually uses.
 */
export class Machine {
  readonly info: MachineInfo;
  private readonly fleet: Fleet;
  private cached: Transport | null = null;

  constructor(fleet: Fleet, info: MachineInfo) {
    this.fleet = fleet;
    this.info = info;
  }

  get deviceId(): string { return this.info.deviceId; }
  get alias(): string | null { return this.info.alias; }
  get tags(): string[] { return this.info.tags; }
  get online(): boolean { return this.info.online; }

  /** Resolve (and cache) the winning transport via the connect.ts ladder. */
  async transport(): Promise<Transport> {
    if (this.cached) return this.cached;
    const o = this.fleet.opts;
    const device: DeviceCoords = {
      deviceId: this.info.deviceId,
      localIps: this.info.localIps,
      quicHost: this.info.quicHost,
      quicPort: this.info.quicPort,
      publicEndpoints: this.info.publicEndpoints,
    };
    const connectOpts = {
      deviceId: this.info.deviceId,
      token: o.token,
      device,
      relay: o.relay ?? null,
      directPort: o.directPort,
      probeTimeoutMs: o.probeTimeoutMs,
    };
    if (buildCandidates(connectOpts).length === 0) {
      throw new Error(`machine ${this.info.deviceId}: no transport (offline / no relay configured)`);
    }
    this.cached = await pickTransport(connectOpts);
    return this.cached;
  }

  /** Run a command, streaming stdout/stderr deltas; the generator's return is the ExecResult. */
  async *exec(command: string, opts: ExecOpts = {}): AsyncGenerator<{ stream: 'stdout' | 'stderr'; text: string }, ExecResult> {
    const t = await this.transport();
    const o = this.fleet.opts;
    const startRes = await transportFetch(t, o.token, '/exec', {
      method: 'POST',
      body: JSON.stringify({ command, workDir: opts.workDir, timeout: opts.timeout, env: opts.env }),
    }, true);
    if (!startRes.ok) {
      return { machine: this.info, code: -1, stdout: '', stderr: '', error: `start exec: HTTP ${startRes.status}` };
    }
    const { execId } = (await startRes.json()) as { execId: string };
    let lastOut = 0, lastErr = 0;
    const poll = opts.pollIntervalMs ?? 300;
    // eslint-disable-next-line no-constant-condition
    while (true) {
      const res = await transportFetch(t, o.token, `/exec/${execId}`);
      if (!res.ok) {
        return { machine: this.info, code: -1, stdout: '', stderr: '', error: `poll exec: HTTP ${res.status}` };
      }
      const { exec } = (await res.json()) as { exec: ExecSessionView };
      if (exec.stdout.length > lastOut) { yield { stream: 'stdout', text: exec.stdout.slice(lastOut) }; lastOut = exec.stdout.length; }
      if (exec.stderr.length > lastErr) { yield { stream: 'stderr', text: exec.stderr.slice(lastErr) }; lastErr = exec.stderr.length; }
      if (exec.status === 'completed' || exec.status === 'failed' || exec.status === 'killed') {
        return { machine: this.info, code: exec.exitCode ?? (exec.status === 'completed' ? 0 : 1), stdout: exec.stdout, stderr: exec.stderr };
      }
      await sleep(poll);
    }
  }

  /** Run a command to completion and collect the result (no streaming). */
  async run(command: string, opts: ExecOpts = {}): Promise<ExecResult> {
    const it = this.exec(command, opts);
    let next = await it.next();
    while (!next.done) next = await it.next();
    return next.value;
  }

  /** Set / add / remove fleet tags on this machine (via Convex /devices/tags). */
  async tag(change: { set?: string[]; add?: string[]; remove?: string[] }): Promise<string[]> {
    const res = await fetch(`${this.fleet.convexUrl}/devices/tags`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.fleet.opts.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ deviceId: this.info.deviceId, tags: change.set, add: change.add, remove: change.remove }),
    });
    if (!res.ok) throw new Error(`tag: HTTP ${res.status}`);
    const { tags } = (await res.json()) as { tags: string[] };
    this.info.tags = tags;
    return tags;
  }
}

/** A resolved set of machines you can fan operations across. */
export class Selection {
  readonly machines: Machine[];
  constructor(machines: Machine[]) { this.machines = machines; }

  get length(): number { return this.machines.length; }
  [Symbol.iterator]() { return this.machines[Symbol.iterator](); }

  /**
   * Fan a command across every machine; merge their streamed output into one
   * async iterator, each line tagged with its source machine. Machines run
   * concurrently — slow boxes don't block fast ones. Each in-flight promise
   * carries its machine's index so the settled one can be re-armed by identity.
   */
  async *exec(command: string, opts: ExecOpts = {}): AsyncGenerator<ExecLine, ExecResult[]> {
    type Delta = { stream: 'stdout' | 'stderr'; text: string };
    const results: ExecResult[] = [];
    const iters = this.machines.map((m) => m.exec(command, opts));
    const inflight = new Map<number, Promise<{ i: number; r: IteratorResult<Delta, ExecResult> }>>();
    iters.forEach((it, i) => inflight.set(i, it.next().then((r) => ({ i, r }))));

    while (inflight.size > 0) {
      const { i, r } = await Promise.race(inflight.values());
      inflight.delete(i);
      if (r.done) {
        results.push(r.value);
      } else {
        yield { machine: this.machines[i].info, stream: r.value.stream, text: r.value.text };
        inflight.set(i, iters[i].next().then((rr) => ({ i, r: rr })));
      }
    }
    return results;
  }

  /** Run a command on every machine to completion; collect all results. */
  async run(command: string, opts: ExecOpts = {}): Promise<ExecResult[]> {
    return Promise.all(this.machines.map((m) => m.run(command, opts)));
  }

  /** Map an async fn over each machine concurrently. */
  async map<T>(fn: (m: Machine) => Promise<T>): Promise<T[]> {
    return Promise.all(this.machines.map(fn));
  }
}

/** The fleet handle. `Fleet.connect()` then `select()`. */
export class Fleet {
  readonly opts: Required<Pick<FleetConnectOptions, 'token'>> & FleetConnectOptions;
  readonly convexUrl: string;

  private constructor(opts: FleetConnectOptions) {
    this.opts = opts;
    this.convexUrl = (opts.convexUrl ?? DEFAULT_CONVEX_URL).replace(/\/+$/, '');
  }

  static async connect(opts: FleetConnectOptions): Promise<Fleet> {
    if (!opts.token) throw new Error('Fleet.connect: token required');
    return new Fleet(opts);
  }

  /** Resolve machines matching a tag/platform/online filter. */
  async select(filter: SelectFilter = {}): Promise<Selection> {
    const res = await fetch(`${this.convexUrl}/devices/select`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.opts.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ tags: filter.tags, match: filter.match, platform: filter.platform, online: filter.online }),
    });
    if (!res.ok) throw new Error(`select: HTTP ${res.status}`);
    const { devices } = (await res.json()) as { devices: MachineInfo[] };
    return new Selection(devices.map((d) => new Machine(this, d)));
  }

  /** Every machine the caller owns. */
  async all(): Promise<Selection> { return this.select({}); }

  /** Resolve a single machine by deviceId or alias (throws if not found). */
  async machine(idOrAlias: string): Promise<Machine> {
    const sel = await this.all();
    const hit = sel.machines.find((m) => m.deviceId === idOrAlias || m.alias === idOrAlias);
    if (!hit) throw new Error(`machine not found: ${idOrAlias}`);
    return hit;
  }
}
