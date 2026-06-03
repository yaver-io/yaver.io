/**
 * connect — the client-side transport fallback ladder, the way Yaver's own
 * web/mobile apps reach an agent:
 *
 *   direct-LAN / tailscale  ->  https-tunnel  ->  relay (/d/{deviceId}/, X-Relay-Password)
 *
 * Races the candidates' /health in parallel; the first healthy transport wins
 * and is cached. The returned AgentSession then talks to the agent THROUGH that
 * transport (tasks + streaming), so callers never deal with reachability.
 *
 * The agent itself stays private (it dials OUT to the relay); the relay is the
 * P2P fallback. The owner token never travels — callers pass a scoped Yaver
 * session/per-device token minted by the broker.
 */
import type { Task, CreateTaskOptions } from './types';
import type { DeviceCoords, RelayServer, YaverSettings } from './discovery';

export type TransportKind = 'direct' | 'tunnel' | 'relay';

export interface Transport {
  kind: TransportKind;
  /** Base URL that agent paths are appended to (no trailing slash). */
  baseURL: string;
  /** Extra headers (e.g. X-Relay-Password). */
  headers: Record<string, string>;
}

export interface AgentRunnerState {
  id: string;
  name?: string;
  installed?: boolean;
  authConfigured?: boolean;
  isDefault?: boolean;
}

export interface AgentStatus {
  transport: TransportKind;
  reachable: boolean;
  accountLinked: boolean;
  runners: AgentRunnerState[];
  defaultRunner: string | null;
  /** Reachable AND at least one runner is authed → ready to trigger work. */
  ready: boolean;
}

export interface ConnectOptions {
  deviceId: string;
  /** Yaver bearer the agent accepts (scoped session / per-device token). */
  token: string;
  /**
   * Mobile/long-session hook: called to mint a fresh token when the current
   * one is rejected (401/403). Lets a backgrounded app recover transparently
   * instead of dying mid-session. Returns the new bearer.
   */
  getToken?: () => Promise<string>;
  /**
   * Client-side defense-in-depth: the runner allowlist from the resolved
   * policy. `createTask` refuses a runner outside it. The agent still enforces
   * authoritatively from the token scope; this is just a fast, honest failure.
   */
  allowedRunners?: string[];
  /** Device coordinates from YaverConvexClient.listDevices(). */
  device?: DeviceCoords;
  /** Relay endpoint + password from getSettings()/getConfig(). */
  relay?: { url: string; password: string } | null;
  /** Extra relay candidates (from /config relayServers) sharing one password. */
  relayServers?: RelayServer[];
  /** HTTPS tunnel front door (publicEndpoints / settings.tunnelUrl). */
  tunnelUrl?: string;
  /** Agent HTTP port for direct-LAN probes (default 18080). */
  directPort?: number;
  /** Force relay only (settings.forceRelay) — skip direct/tunnel. */
  forceRelay?: boolean;
  /** Per-candidate health timeout (ms). */
  probeTimeoutMs?: number;
  /** Reuse a previously-cached winning transport (skips the race if healthy). */
  cached?: Transport | null;
}

/** Build the ordered candidate transports for a device. */
export function buildCandidates(opts: ConnectOptions): Transport[] {
  const out: Transport[] = [];
  const port = opts.directPort ?? 18080;
  if (!opts.forceRelay) {
    // direct-LAN / tailscale — every advertised local IP, then quicHost.
    const hosts = new Set<string>();
    for (const ip of opts.device?.localIps ?? []) if (ip) hosts.add(ip);
    if (opts.device?.quicHost) hosts.add(opts.device.quicHost);
    for (const h of hosts) {
      const hostPart = h.includes(':') ? `[${h}]` : h; // bracket IPv6
      out.push({ kind: 'direct', baseURL: `http://${hostPart}:${port}`, headers: {} });
    }
    // https-tunnel — public endpoints + explicit tunnelUrl.
    const tunnels = new Set<string>();
    for (const e of opts.device?.publicEndpoints ?? []) if (e) tunnels.add(e);
    if (opts.tunnelUrl) tunnels.add(opts.tunnelUrl);
    for (const t of tunnels) out.push({ kind: 'tunnel', baseURL: t.replace(/\/+$/, ''), headers: {} });
  }
  // relay (P2P fallback) — primary relay + any extra relay servers.
  const relays: Array<{ url: string; password: string }> = [];
  if (opts.relay?.url && opts.relay?.password) relays.push(opts.relay);
  for (const r of opts.relayServers ?? []) {
    if (r.httpUrl && opts.relay?.password) relays.push({ url: r.httpUrl, password: opts.relay.password });
  }
  for (const r of relays) {
    out.push({
      kind: 'relay',
      baseURL: `${r.url.replace(/\/+$/, '')}/d/${encodeURIComponent(opts.deviceId)}`,
      headers: { 'X-Relay-Password': r.password },
    });
  }
  return out;
}

async function probe(t: Transport, token: string, timeoutMs: number): Promise<boolean> {
  try {
    const res = await fetch(`${t.baseURL}/health`, {
      headers: { Authorization: `Bearer ${token}`, ...t.headers },
      signal: AbortSignal.timeout(timeoutMs),
    });
    return res.ok;
  } catch {
    return false;
  }
}

/** Race all candidates' /health; the first healthy transport wins. */
export async function pickTransport(opts: ConnectOptions): Promise<Transport> {
  const timeout = opts.probeTimeoutMs ?? 4000;
  if (opts.cached && (await probe(opts.cached, opts.token, timeout))) return opts.cached;
  const candidates = buildCandidates(opts);
  if (candidates.length === 0) throw new Error('No transports available (no device coords, tunnel, or relay)');
  // First healthy wins; resolve as soon as any probe succeeds.
  return new Promise<Transport>((resolve, reject) => {
    let pending = candidates.length;
    let settled = false;
    for (const c of candidates) {
      probe(c, opts.token, timeout).then((ok) => {
        if (ok && !settled) { settled = true; resolve(c); }
        if (--pending === 0 && !settled) reject(new Error('Agent unreachable on all transports'));
      });
    }
  });
}

/** A connected agent session bound to a winning transport. */
export class AgentSession {
  readonly transport: Transport;
  private _token: string;
  private readonly getToken?: () => Promise<string>;
  private readonly allowedRunners?: string[];

  constructor(
    transport: Transport,
    token: string,
    opts?: { getToken?: () => Promise<string>; allowedRunners?: string[] },
  ) {
    this.transport = transport;
    this._token = token;
    this.getToken = opts?.getToken;
    this.allowedRunners = opts?.allowedRunners;
  }

  /** Current bearer (may rotate over the session's life via the refresh hook). */
  get token(): string { return this._token; }

  private headers(json = false): Record<string, string> {
    return {
      Authorization: `Bearer ${this._token}`,
      ...(json ? { 'Content-Type': 'application/json' } : {}),
      ...this.transport.headers,
    };
  }

  /**
   * fetch with one transparent token refresh on 401/403. Without a getToken
   * hook this behaves like a plain authed fetch.
   */
  private async authedFetch(path: string, init: RequestInit = {}, json = false): Promise<Response> {
    const run = () => fetch(`${this.transport.baseURL}${path}`, { ...init, headers: { ...this.headers(json), ...(init.headers as Record<string, string> | undefined) } });
    let res = await run();
    if ((res.status === 401 || res.status === 403) && this.getToken) {
      try { this._token = await this.getToken(); } catch { return res; }
      res = await run();
    }
    return res;
  }

  async health(): Promise<boolean> {
    try { return (await this.authedFetch('/health')).ok; }
    catch { return false; }
  }

  /**
   * High-level state: is the agent reachable, is its account linked, and which
   * runners are ready. Consumers use this to gate UI without knowing internals.
   */
  async status(): Promise<AgentStatus> {
    const get = async <T>(p: string, auth = true): Promise<T | null> => {
      try {
        const res = auth
          ? await this.authedFetch(p)
          : await fetch(`${this.transport.baseURL}${p}`, { headers: this.transport.headers });
        return res.ok ? (await res.json() as T) : null;
      } catch { return null; }
    };
    const [health, account, runnersResp] = await Promise.all([
      get<{ status?: string }>('/health', false),
      get<Record<string, unknown>>('/auth/status', false),
      get<{ runners?: AgentStatus['runners']; default?: string }>('/agent/runners'),
    ]);
    const runners = runnersResp?.runners ?? [];
    return {
      transport: this.transport.kind,
      reachable: Boolean(health),
      accountLinked: readAuthed(account),
      runners,
      defaultRunner: runnersResp?.default ?? runners.find((r) => r.isDefault)?.id ?? null,
      ready: Boolean(health) && runners.some((r) => r.authConfigured),
    };
  }

  async createTask(prompt: string, opts?: CreateTaskOptions & { source?: string }): Promise<Task> {
    if (opts?.runner && this.allowedRunners && this.allowedRunners.length > 0 && !this.allowedRunners.includes(opts.runner)) {
      throw new Error(`runner "${opts.runner}" is not permitted by policy (allowed: ${this.allowedRunners.join(', ')})`);
    }
    const body: Record<string, unknown> = { title: prompt, source: opts?.source ?? 'sdk' };
    if (opts?.model) body.model = opts.model;
    if (opts?.runner) body.runner = opts.runner;
    if (opts?.images?.length) body.images = opts.images;
    const res = await this.authedFetch('/tasks', { method: 'POST', body: JSON.stringify(body) }, true);
    if (!res.ok) throw new Error(`createTask -> HTTP ${res.status}`);
    const r = await res.json() as { ok?: boolean; taskId?: string; status?: string; runnerId?: string; error?: string };
    if (r.ok === false || !r.taskId) throw new Error(r.error || 'Failed to create task');
    return { id: r.taskId, title: prompt, status: (r.status as Task['status']) ?? 'queued', runnerId: r.runnerId, createdAt: new Date(0).toISOString() };
  }

  async getTask(taskId: string): Promise<Task> {
    const res = await this.authedFetch(`/tasks/${encodeURIComponent(taskId)}`);
    if (!res.ok) throw new Error(`getTask -> HTTP ${res.status}`);
    const r = await res.json() as { task: Task };
    return r.task;
  }

  async stopTask(taskId: string): Promise<void> {
    await this.authedFetch(`/tasks/${encodeURIComponent(taskId)}/stop`, { method: 'POST' });
  }

  /**
   * Stream task output. Prefers the agent's server-sent stream
   * (`/tasks/{id}/output`) — cheap on mobile/cellular — and falls back to
   * polling getTask when the stream isn't available or drops.
   */
  async *streamOutput(taskId: string, pollIntervalMs = 1000): AsyncGenerator<string> {
    const sseOk = yield* this.streamOutputSSE(taskId);
    if (sseOk) return;
    yield* this.streamOutputPolling(taskId, pollIntervalMs);
  }

  /** SSE path. Yields chunks; returns true if it ran to a terminal state. */
  private async *streamOutputSSE(taskId: string): AsyncGenerator<string, boolean> {
    let res: Response;
    try {
      res = await this.authedFetch(`/tasks/${encodeURIComponent(taskId)}/output`, { headers: { Accept: 'text/event-stream' } });
    } catch { return false; }
    const ctype = res.headers.get('content-type') ?? '';
    if (!res.ok || !res.body || !ctype.includes('text/event-stream')) return false;
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    const decoder = new TextDecoder();
    let buf = '';
    let sawData = false;
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const events = buf.split('\n\n');
        buf = events.pop() ?? '';
        for (const ev of events) {
          for (const line of ev.split('\n')) {
            if (!line.startsWith('data:')) continue;
            const data = line.slice(5).replace(/^ /, '');
            if (data === '[DONE]') return true;
            sawData = true;
            yield data;
          }
        }
      }
    } catch {
      return sawData; // partial stream: if we got anything, let caller decide; treat as handled
    } finally {
      try { reader.releaseLock(); } catch { /* noop */ }
    }
    return sawData;
  }

  /** Polling fallback — re-GET the task and diff the output tail. */
  private async *streamOutputPolling(taskId: string, pollIntervalMs: number): AsyncGenerator<string> {
    let lastLen = 0;
    for (;;) {
      const task = await this.getTask(taskId);
      const out = task.output ?? '';
      if (out.length > lastLen) { yield out.slice(lastLen); lastLen = out.length; }
      if (task.status === 'completed' || task.status === 'failed' || task.status === 'stopped') return;
      await new Promise((r) => setTimeout(r, pollIntervalMs));
    }
  }
}

/** Discover the best transport and return a ready AgentSession. */
export async function connect(opts: ConnectOptions): Promise<AgentSession> {
  const transport = await pickTransport(opts);
  return new AgentSession(transport, opts.token, { getToken: opts.getToken, allowedRunners: opts.allowedRunners });
}

/**
 * Connect using an opaque handle from the server broker (YaverApp.sessionHandle).
 * The consumer passes the handle straight through — it never has to know about
 * relays, tunnels, or Convex. `token` may be on the handle or passed explicitly.
 */
export function connectHandle(
  handle: {
    deviceId: string;
    token?: string;
    device?: DeviceCoords | null;
    relay?: { url: string; password: string } | null;
    relayServers?: RelayServer[];
    tunnelUrl?: string;
    forceRelay?: boolean;
    /** Allowed-runner scope baked in by the server (YaverApp.resolvedHandle). */
    allowedRunners?: string[];
  },
  opts?: {
    token?: string;
    directPort?: number;
    probeTimeoutMs?: number;
    cached?: Transport | null;
    /** Mobile token-refresh hook (re-mint a scoped token on expiry). */
    getToken?: () => Promise<string>;
    /** Override the handle's runner scope (rarely needed). */
    allowedRunners?: string[];
  },
): Promise<AgentSession> {
  const token = opts?.token ?? handle.token;
  if (!token) throw new Error('connectHandle: a token is required (on the handle or in opts)');
  return connect({
    deviceId: handle.deviceId,
    token,
    device: handle.device ?? undefined,
    relay: handle.relay ?? null,
    relayServers: handle.relayServers,
    tunnelUrl: handle.tunnelUrl,
    forceRelay: handle.forceRelay,
    directPort: opts?.directPort,
    probeTimeoutMs: opts?.probeTimeoutMs,
    cached: opts?.cached ?? null,
    getToken: opts?.getToken,
    allowedRunners: opts?.allowedRunners ?? handle.allowedRunners,
  });
}

/** Tolerant read of the agent's /auth/status across versions. */
function readAuthed(account: Record<string, unknown> | null): boolean {
  if (!account) return false;
  if (typeof account.authenticated === 'boolean') return account.authenticated as boolean;
  if (typeof account.authed === 'boolean') return account.authed as boolean;
  if (typeof account.loggedIn === 'boolean') return account.loggedIn as boolean;
  if (account.user && typeof account.user === 'object') return true;
  if (typeof account.email === 'string' && account.email) return true;
  if (typeof account.status === 'string') return ['authenticated', 'ok', 'linked', 'ready'].includes((account.status as string).toLowerCase());
  return false;
}
