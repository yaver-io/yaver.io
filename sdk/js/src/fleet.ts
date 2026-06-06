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

/** A mutating fleet action, surfaced to the approval gate + audit sink. */
export interface ActionEvent {
  kind: 'exec' | 'agent' | 'apply' | 'upload';
  machine: MachineInfo;
  risk: 'low' | 'high';
  command?: string; // exec
  prompt?: string;  // agent
  key?: string;     // apply (idempotency key)
  path?: string;    // upload (remote path)
}

/** An action's outcome, emitted to the audit sink after it runs. */
export interface AuditEvent extends ActionEvent {
  at: number; // epoch ms
  outcome: 'ok' | 'denied' | 'error';
  detail?: string;
}

// Destructive-by-default patterns: rm -rf, mkfs, dd, fork bombs, chmod -R 777,
// writes to raw block devices, DB drops, force-push, reboot/shutdown.
const DEFAULT_RISK =
  /(\brm\s+-rf?\b|\bmkfs\b|\bdd\s+if=|:\(\)\s*\{|\bchmod\s+-R\s+777\b|>\s*\/dev\/sd|\bdrop\s+(table|database)\b|\bgit\s+push\b.*--force|\b(reboot|shutdown|halt|poweroff)\b)/i;

function defaultClassifyRisk(command: string): 'low' | 'high' {
  return DEFAULT_RISK.test(command) ? 'high' : 'low';
}

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
  /**
   * Human-in-the-loop gate. Called before a mutating action (agent dispatch,
   * apply, upload, and HIGH-risk exec). Return false to block it; the action
   * resolves to a denied outcome rather than running. Low-risk exec is not gated.
   */
  approve?: (ev: ActionEvent) => boolean | Promise<boolean>;
  /** Audit sink — called after every action with its outcome. Never throws into the action. */
  onAudit?: (ev: AuditEvent) => void;
  /** Override the exec risk classifier (default flags destructive patterns). */
  classifyRisk?: (command: string) => 'low' | 'high';
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

export interface AgentOpts {
  /** Coding runner to spawn: 'claude-code' | 'codex' | 'opencode' (agent default if omitted). */
  runner?: string;
  model?: string;
  /** Output poll interval (ms). */
  pollIntervalMs?: number;
}

/** One chunk of an agent session's output, tagged with its source machine. */
export interface AgentLine {
  machine: MachineInfo;
  text: string;
}

/**
 * A verified, reversible remote mutation. The difference between "an agent ran
 * a command" and "an agent made a checked, idempotent, rollback-safe change":
 *   precheck (already in desired state? skip) → do → verify → commit | rollback.
 */
export interface VerifiedAction {
  /** Idempotency / natural key — identifies the change (for logging + skip). */
  key: string;
  /** Command that performs the change. */
  do: string;
  /** Command whose success (+ optional `expect`) confirms the change took. */
  verify: string;
  /**
   * Verify passes when: exit 0 AND (expect is a string → stdout includes it;
   * expect is a predicate → it returns true; expect omitted → exit 0 alone).
   * Also used by `precheck`.
   */
  expect?: string | ((stdout: string) => boolean);
  /** Optional pre-check (same semantics as verify): if it already passes, skip `do`. */
  precheck?: string;
  /** Command to undo the change when verify fails and onFail==='rollback'. */
  rollback?: string;
  /** Retry do+verify up to N times before giving up (default 1). */
  retries?: number;
  /** What to do when verify fails: throw (default), rollback, or leave as-is. */
  onFail?: 'throw' | 'rollback' | 'leave';
}

export interface ApplyResult {
  machine: MachineInfo;
  key: string;
  status: 'verified' | 'already' | 'rolled-back' | 'failed';
  attempts: number;
  detail?: string;
}

/** Terminal result of an agent run on one machine. */
export interface AgentResult {
  machine: MachineInfo;
  taskId: string;
  status: 'completed' | 'failed' | 'stopped' | 'queued' | 'running';
  output: string;
  resultText?: string;
  costUsd?: number;
  error?: string;
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
const nowMs = () => Date.now();

export type ServiceAction = 'restart' | 'start' | 'stop' | 'status';

/**
 * Build the platform-native service-control command. Linux → systemctl,
 * Windows → sc, macOS → launchctl (best-effort: kickstart for restart, print
 * for status, bootstrap/bootout for start/stop in the system domain; adjust
 * the domain target for a gui/user service). Exported + pure so it's unit-tested.
 */
export function serviceCmd(platform: string, action: ServiceAction, name: string): string {
  switch (platform) {
    case 'windows':
      return `sc ${action === 'status' ? 'query' : action} ${name}`;
    case 'macos':
      switch (action) {
        case 'restart': return `launchctl kickstart -k system/${name}`;
        case 'status':  return `launchctl print system/${name}`;
        case 'start':   return `launchctl bootstrap system /Library/LaunchDaemons/${name}.plist`;
        case 'stop':    return `launchctl bootout system/${name}`;
      }
      return `launchctl print system/${name}`;
    default: // linux + any systemd host
      return `systemctl ${action} ${name}`;
  }
}

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

  /** Run the approval gate for a mutating action. True ⇒ proceed. */
  private async guard(ev: ActionEvent): Promise<boolean> {
    const approve = this.fleet.opts.approve;
    if (!approve) return true;
    if (ev.kind === 'exec' && ev.risk !== 'high') return true; // only gate risky exec
    return Boolean(await approve(ev));
  }

  /** Emit an audit event. A throwing sink must never break the action. */
  private audit(ev: Omit<AuditEvent, 'at'>): void {
    const sink = this.fleet.opts.onAudit;
    if (!sink) return;
    try { sink({ ...ev, at: nowMs() }); } catch { /* audit must not break the action */ }
  }

  private classifyRisk(command: string): 'low' | 'high' {
    return (this.fleet.opts.classifyRisk ?? defaultClassifyRisk)(command);
  }

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
    const risk = this.classifyRisk(command);
    if (!(await this.guard({ kind: 'exec', machine: this.info, risk, command }))) {
      this.audit({ kind: 'exec', machine: this.info, risk, command, outcome: 'denied' });
      return { machine: this.info, code: -1, stdout: '', stderr: '', error: 'denied by approval gate' };
    }
    const t = await this.transport();
    const o = this.fleet.opts;
    const startRes = await transportFetch(t, o.token, '/exec', {
      method: 'POST',
      body: JSON.stringify({ command, workDir: opts.workDir, timeout: opts.timeout, env: opts.env }),
    }, true);
    if (!startRes.ok) {
      const detail = `start exec: HTTP ${startRes.status}`;
      this.audit({ kind: 'exec', machine: this.info, risk, command, outcome: 'error', detail });
      return { machine: this.info, code: -1, stdout: '', stderr: '', error: detail };
    }
    const { execId } = (await startRes.json()) as { execId: string };
    let lastOut = 0, lastErr = 0;
    const poll = opts.pollIntervalMs ?? 300;
    // eslint-disable-next-line no-constant-condition
    while (true) {
      const res = await transportFetch(t, o.token, `/exec/${execId}`);
      if (!res.ok) {
        const detail = `poll exec: HTTP ${res.status}`;
        this.audit({ kind: 'exec', machine: this.info, risk, command, outcome: 'error', detail });
        return { machine: this.info, code: -1, stdout: '', stderr: '', error: detail };
      }
      const { exec } = (await res.json()) as { exec: ExecSessionView };
      if (exec.stdout.length > lastOut) { yield { stream: 'stdout', text: exec.stdout.slice(lastOut) }; lastOut = exec.stdout.length; }
      if (exec.stderr.length > lastErr) { yield { stream: 'stderr', text: exec.stderr.slice(lastErr) }; lastErr = exec.stderr.length; }
      if (exec.status === 'completed' || exec.status === 'failed' || exec.status === 'killed') {
        const code = exec.exitCode ?? (exec.status === 'completed' ? 0 : 1);
        this.audit({ kind: 'exec', machine: this.info, risk, command, outcome: code === 0 ? 'ok' : 'error' });
        return { machine: this.info, code, stdout: exec.stdout, stderr: exec.stderr };
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

  /**
   * Dispatch an autonomous coding agent (claude-code / codex / opencode) to
   * this machine on `prompt`, streaming its session output. The generator's
   * return is the terminal AgentResult. This is "run agent on machine N" — it
   * creates a task the remote runner works on (over the resolved transport)
   * and tails it; no SSH, no manual attach.
   */
  async *agent(prompt: string, opts: AgentOpts = {}): AsyncGenerator<{ text: string }, AgentResult> {
    // Agent dispatch is autonomous → always gateable (treated as high-risk).
    if (!(await this.guard({ kind: 'agent', machine: this.info, risk: 'high', prompt }))) {
      this.audit({ kind: 'agent', machine: this.info, risk: 'high', prompt, outcome: 'denied' });
      return { machine: this.info, taskId: '', status: 'failed', output: '', error: 'denied by approval gate' };
    }
    const t = await this.transport();
    const o = this.fleet.opts;
    const startRes = await transportFetch(t, o.token, '/tasks', {
      method: 'POST',
      body: JSON.stringify({ title: prompt, runner: opts.runner, model: opts.model }),
    }, true);
    if (!startRes.ok) {
      const detail = `dispatch: HTTP ${startRes.status}`;
      this.audit({ kind: 'agent', machine: this.info, risk: 'high', prompt, outcome: 'error', detail });
      return { machine: this.info, taskId: '', status: 'failed', output: '', error: detail };
    }
    const { taskId } = (await startRes.json()) as { taskId: string };
    let lastLen = 0;
    const poll = opts.pollIntervalMs ?? 800;
    // eslint-disable-next-line no-constant-condition
    while (true) {
      const res = await transportFetch(t, o.token, `/tasks/${taskId}`);
      if (!res.ok) {
        const detail = `poll task: HTTP ${res.status}`;
        this.audit({ kind: 'agent', machine: this.info, risk: 'high', prompt, outcome: 'error', detail });
        return { machine: this.info, taskId, status: 'failed', output: '', error: detail };
      }
      const { task } = (await res.json()) as { task: { status: string; output?: string; resultText?: string; costUsd?: number } };
      const output = task.output ?? '';
      if (output.length > lastLen) { yield { text: output.slice(lastLen) }; lastLen = output.length; }
      if (task.status === 'completed' || task.status === 'failed' || task.status === 'stopped') {
        this.audit({ kind: 'agent', machine: this.info, risk: 'high', prompt, outcome: task.status === 'completed' ? 'ok' : 'error' });
        return { machine: this.info, taskId, status: task.status as AgentResult['status'], output, resultText: task.resultText, costUsd: task.costUsd };
      }
      await sleep(poll);
    }
  }

  /**
   * Upload a local file to an absolute path on this machine, over the resolved
   * transport (so it works behind NAT via relay/mesh). Preserves the local
   * file's permission bits. Node-only (dynamically imports node:fs).
   */
  async upload(localPath: string, remotePath: string): Promise<{ bytes: number }> {
    if (!(await this.guard({ kind: 'upload', machine: this.info, risk: 'high', path: remotePath }))) {
      this.audit({ kind: 'upload', machine: this.info, risk: 'high', path: remotePath, outcome: 'denied' });
      throw new Error(`upload ${remotePath}: denied by approval gate`);
    }
    const fs = await import('node:fs/promises');
    const data = await fs.readFile(localPath);
    const mode = ((await fs.stat(localPath)).mode & 0o777).toString(8);
    const t = await this.transport();
    const res = await transportFetch(
      t, this.fleet.opts.token,
      `/fleet/file?path=${encodeURIComponent(remotePath)}&mode=${mode}`,
      { method: 'POST', body: new Uint8Array(data) },
    );
    if (!res.ok) {
      this.audit({ kind: 'upload', machine: this.info, risk: 'high', path: remotePath, outcome: 'error', detail: `HTTP ${res.status}` });
      throw new Error(`upload ${remotePath}: HTTP ${res.status}`);
    }
    const bytes = (await res.json() as { bytes: number }).bytes;
    this.audit({ kind: 'upload', machine: this.info, risk: 'high', path: remotePath, outcome: 'ok', detail: `${bytes} bytes` });
    return { bytes };
  }

  /** Download an absolute remote file to a local path (creating parent dirs). */
  async download(remotePath: string, localPath: string): Promise<{ bytes: number }> {
    const fs = await import('node:fs/promises');
    const path = await import('node:path');
    const t = await this.transport();
    const res = await transportFetch(t, this.fleet.opts.token, `/fleet/file?path=${encodeURIComponent(remotePath)}`);
    if (!res.ok) throw new Error(`download ${remotePath}: HTTP ${res.status}`);
    const bytes = new Uint8Array(await res.arrayBuffer());
    await fs.mkdir(path.dirname(localPath), { recursive: true });
    await fs.writeFile(localPath, bytes);
    const mode = res.headers.get('X-Yaver-File-Mode');
    if (mode) { try { await fs.chmod(localPath, parseInt(mode, 8)); } catch { /* best-effort */ } }
    return { bytes: bytes.length };
  }

  /** Recursively upload a local directory tree to a remote directory. */
  async uploadDir(localDir: string, remoteDir: string): Promise<{ files: number; bytes: number }> {
    const fs = await import('node:fs/promises');
    const path = await import('node:path');
    const remoteBase = remoteDir.replace(/\/+$/, '');
    let files = 0, bytes = 0;
    const walk = async (rel: string): Promise<void> => {
      const entries = await fs.readdir(path.join(localDir, rel), { withFileTypes: true });
      for (const e of entries) {
        const childRel = rel ? `${rel}/${e.name}` : e.name;
        if (e.isDirectory()) { await walk(childRel); continue; }
        if (!e.isFile()) continue;
        const r = await this.upload(path.join(localDir, ...childRel.split('/')), `${remoteBase}/${childRel}`);
        files++; bytes += r.bytes;
      }
    };
    await walk('');
    return { files, bytes };
  }

  /** Run a check command; ok = exit 0 AND (expect satisfied, if given). */
  private async check(command: string, expect?: VerifiedAction['expect']): Promise<{ ok: boolean; out: string }> {
    const r = await this.run(command);
    let ok = r.code === 0;
    if (ok && expect !== undefined) ok = typeof expect === 'function' ? expect(r.stdout) : r.stdout.includes(expect);
    return { ok, out: r.stdout || r.stderr };
  }

  /**
   * Apply a verified, reversible mutation: skip if already in the desired state
   * (idempotency), else do → verify → commit | rollback. Throws on failure
   * unless onFail is 'rollback' or 'leave'. This is the safety primitive that
   * makes autonomous remote changes trustworthy.
   */
  async apply(action: VerifiedAction): Promise<ApplyResult> {
    if (action.precheck) {
      const pre = await this.check(action.precheck, action.expect);
      if (pre.ok) {
        this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'ok', detail: 'already' });
        return { machine: this.info, key: action.key, status: 'already', attempts: 0 };
      }
    }
    if (!(await this.guard({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, command: action.do }))) {
      this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'denied' });
      return { machine: this.info, key: action.key, status: 'failed', attempts: 0, detail: 'denied by approval gate' };
    }
    const maxAttempts = Math.max(1, action.retries ?? 1);
    let lastDetail = '';
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      const did = await this.run(action.do);
      const v = await this.check(action.verify, action.expect);
      lastDetail = (v.out || did.stderr || '').slice(0, 500);
      if (v.ok) {
        this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'ok' });
        return { machine: this.info, key: action.key, status: 'verified', attempts: attempt };
      }
    }
    const onFail = action.onFail ?? 'throw';
    if (onFail === 'rollback' && action.rollback) {
      await this.run(action.rollback);
      this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'error', detail: `rolled-back: ${lastDetail}` });
      return { machine: this.info, key: action.key, status: 'rolled-back', attempts: maxAttempts, detail: lastDetail };
    }
    if (onFail === 'leave') {
      this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'error', detail: lastDetail });
      return { machine: this.info, key: action.key, status: 'failed', attempts: maxAttempts, detail: lastDetail };
    }
    this.audit({ kind: 'apply', machine: this.info, risk: 'high', key: action.key, outcome: 'error', detail: lastDetail });
    throw new Error(`apply '${action.key}' on ${this.info.deviceId}: verify failed after ${maxAttempts} attempt(s)${lastDetail ? ` — ${lastDetail}` : ''}`);
  }

  /**
   * Restart an OS service — platform-aware (systemd / launchd / Windows SC).
   * Composes exec, so it's covered by the approval gate + audit like any other
   * action. For start/stop/status use service() with the action.
   */
  serviceRestart(name: string): Promise<ExecResult> { return this.service('restart', name); }
  service(action: ServiceAction, name: string): Promise<ExecResult> {
    return this.run(serviceCmd(this.info.platform, action, name));
  }

  /**
   * Reboot the machine. The command matches the default high-risk classifier,
   * so a Fleet configured with an `approve` gate will require confirmation.
   */
  reboot(): Promise<ExecResult> {
    return this.run(this.info.platform === 'windows' ? 'shutdown /r /t 0' : 'sudo reboot');
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

/**
 * Merge per-machine async generators into one stream: yield each delta tagged
 * by its machine, collect each generator's return value. Concurrent — every
 * in-flight promise carries its machine's index so the settled one is re-armed
 * by identity, and a slow machine never blocks a fast one.
 */
async function* mergeFanout<D, R, L>(
  machines: Machine[],
  make: (m: Machine) => AsyncGenerator<D, R>,
  tag: (info: MachineInfo, delta: D) => L,
): AsyncGenerator<L, R[]> {
  const results: R[] = [];
  const iters = machines.map(make);
  const inflight = new Map<number, Promise<{ i: number; r: IteratorResult<D, R> }>>();
  iters.forEach((it, i) => inflight.set(i, it.next().then((r) => ({ i, r }))));
  while (inflight.size > 0) {
    const { i, r } = await Promise.race(inflight.values());
    inflight.delete(i);
    if (r.done) {
      results.push(r.value);
    } else {
      yield tag(machines[i].info, r.value);
      inflight.set(i, iters[i].next().then((rr) => ({ i, r: rr })));
    }
  }
  return results;
}

/** A resolved set of machines you can fan operations across. */
export class Selection {
  readonly machines: Machine[];
  constructor(machines: Machine[]) { this.machines = machines; }

  get length(): number { return this.machines.length; }
  [Symbol.iterator]() { return this.machines[Symbol.iterator](); }

  /**
   * Fan a command across every machine; merge their streamed output into one
   * async iterator, each line tagged with its source machine. Concurrent — slow
   * boxes don't block fast ones. The generator's return is one ExecResult per machine.
   */
  exec(command: string, opts: ExecOpts = {}): AsyncGenerator<ExecLine, ExecResult[]> {
    return mergeFanout(
      this.machines,
      (m) => m.exec(command, opts),
      (machine, d): ExecLine => ({ machine, stream: d.stream, text: d.text }),
    );
  }

  /**
   * Dispatch an autonomous agent to every machine on the same prompt; merge
   * their session output tagged by machine. The generator's return is one
   * AgentResult per machine. This is fleet-wide "send this work to N boxes."
   */
  agent(prompt: string, opts: AgentOpts = {}): AsyncGenerator<AgentLine, AgentResult[]> {
    return mergeFanout(
      this.machines,
      (m) => m.agent(prompt, opts),
      (machine, d): AgentLine => ({ machine, text: d.text }),
    );
  }

  /** Run a command on every machine to completion; collect all results. */
  async run(command: string, opts: ExecOpts = {}): Promise<ExecResult[]> {
    return Promise.all(this.machines.map((m) => m.run(command, opts)));
  }

  /** Restart a service on every machine concurrently (e.g. recycle a runner pool). */
  async serviceRestart(name: string): Promise<ExecResult[]> {
    return Promise.all(this.machines.map((m) => m.serviceRestart(name)));
  }

  /** Upload a local file to the same absolute path on every machine concurrently. */
  async upload(localPath: string, remotePath: string): Promise<Array<{ machine: MachineInfo; bytes: number }>> {
    return Promise.all(this.machines.map(async (m) => ({ machine: m.info, ...(await m.upload(localPath, remotePath)) })));
  }

  /**
   * Apply a verified mutation across every machine, collecting one ApplyResult
   * each. Fleet semantics: a single machine throwing never aborts the others —
   * its outcome is captured as status 'failed' with the error detail.
   */
  async apply(action: VerifiedAction): Promise<ApplyResult[]> {
    return Promise.all(this.machines.map(async (m) => {
      try {
        return await m.apply(action);
      } catch (e) {
        return { machine: m.info, key: action.key, status: 'failed' as const, attempts: 0, detail: e instanceof Error ? e.message : String(e) };
      }
    }));
  }

  /** Map an async fn over each machine concurrently. */
  async map<T>(fn: (m: Machine) => Promise<T>): Promise<T[]> {
    return Promise.all(this.machines.map(fn));
  }

  /**
   * Commander/worker fan-out: distribute a work-list across the machines with
   * work-stealing — each machine pulls the next item as soon as it's free, up
   * to `concurrency` items in flight per machine. Results come back in input
   * order. Wall-clock ≈ the busiest machine's chain, not the slowest item ×
   * count. The general "spread N tasks over my fleet and aggregate" primitive.
   */
  async distribute<I, O>(
    items: I[],
    worker: (machine: Machine, item: I, index: number) => Promise<O>,
    opts: { concurrency?: number } = {},
  ): Promise<O[]> {
    if (this.machines.length === 0) throw new Error('distribute: no machines in selection');
    const results = new Array<O>(items.length);
    const conc = Math.max(1, opts.concurrency ?? 1);
    let next = 0; // shared cursor; ++ is atomic between awaits (single-threaded)
    const pull = async (m: Machine): Promise<void> => {
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const i = next++;
        if (i >= items.length) return;
        results[i] = await worker(m, items[i], i);
      }
    };
    const runners: Promise<void>[] = [];
    for (const m of this.machines) for (let k = 0; k < conc; k++) runners.push(pull(m));
    await Promise.all(runners);
    return results;
  }

  /** Map an async fn over every machine, then fold the results into one value. */
  async mapReduce<T, R>(
    map: (m: Machine) => Promise<T>,
    reduce: (acc: R, value: T, machine: Machine) => R,
    init: R,
  ): Promise<R> {
    const values = await Promise.all(this.machines.map(map));
    return values.reduce((acc, v, i) => reduce(acc, v, this.machines[i]), init);
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

/**
 * A ready-made audit sink that appends each AuditEvent as one JSON line to a
 * file — the client-side record plane for every fleet action (who ran what,
 * where, with what outcome). Node-only; pass to Fleet.connect({ onAudit }).
 *
 * @example Fleet.connect({ token, onAudit: fileAuditSink('/var/log/yaver-fleet.jsonl') })
 */
export function fileAuditSink(path: string): (ev: AuditEvent) => void {
  return (ev: AuditEvent) => {
    // Fire-and-forget append; never blocks or throws into the action.
    void import('node:fs/promises')
      .then((fs) => fs.appendFile(path, JSON.stringify({
        at: ev.at, kind: ev.kind, outcome: ev.outcome,
        deviceId: ev.machine.deviceId, alias: ev.machine.alias, risk: ev.risk,
        command: ev.command, prompt: ev.prompt, key: ev.key, filePath: ev.path,
        detail: ev.detail,
      }) + '\n'))
      .catch(() => { /* a broken sink must never break the fleet action */ });
  };
}
