import type {
  Task, CreateTaskOptions, AgentInfo, ImageAttachment, ExecSession, ExecOptions,
  RunnerInfo, RunnerAuthSession, RunnerSetupOptions, YaverCapability, AccountLinkSession,
} from './types';
import type { ScreenlogAPI, ScreenlogConfig, ScreenlogPolicy, InputEvent } from './screenlog';

/**
 * Yaver client — connects to a Yaver agent's HTTP API.
 * Works in Node.js, React Native, and browsers.
 */
export class YaverClient {
  baseURL: string;
  authToken: string;
  timeout: number;

  constructor(baseURL: string, authToken: string, timeout = 30000) {
    this.baseURL = baseURL.replace(/\/$/, '');
    this.authToken = authToken;
    this.timeout = timeout;
  }

  /** Check if the agent is reachable. */
  async health(): Promise<{ status: string }> {
    return this.get('/health');
  }

  /**
   * Screen Monitor (screenlog) — local screen-frame black box on this
   * agent's machine. See ./screenlog.ts. Lets talos / any SDK consumer
   * drive recording, pull the smart activity report, and feed an
   * input-event trace. Everything stays local to the recorded machine.
   */
  get screenlog(): ScreenlogAPI {
    return {
      drivers: () => this.get('/screenlog/drivers'),
      start: (config?: ScreenlogConfig, title?: string) =>
        this.post('/screenlog/start', { title, config }),
      stop: () => this.post('/screenlog/stop', {}),
      status: () => this.get('/screenlog/status'),
      list: () => this.get('/screenlog/list'),
      analyze: (id: string, opts?: { idleGapSec?: number; maxAttributeSec?: number }) => {
        const q = new URLSearchParams({ id });
        if (opts?.idleGapSec) q.set('idle_gap_sec', String(opts.idleGapSec));
        if (opts?.maxAttributeSec) q.set('max_attribute_sec', String(opts.maxAttributeSec));
        return this.get(`/screenlog/analyze?${q.toString()}`);
      },
      frames: (id: string) => this.get(`/screenlog/${encodeURIComponent(id)}/frames.json`),
      events: (id: string) => this.get(`/screenlog/${encodeURIComponent(id)}/events`),
      ingestEvents: (id: string, events: InputEvent[]) =>
        this.post(`/screenlog/${encodeURIComponent(id)}/events`, { events }),
      policyGet: () => this.get('/screenlog/policy'),
      policySet: (patch: ScreenlogPolicy) => this.post('/screenlog/policy', patch),
      audit: () => this.get('/screenlog/audit'),
      frameUrl: (id: string, file: string) =>
        `${this.baseURL}/screenlog/${encodeURIComponent(id)}/${file}`,
      exportUrl: (id: string) => `${this.baseURL}/screenlog/${encodeURIComponent(id)}/export`,
    };
  }

  /** Measure round-trip time in milliseconds. */
  async ping(): Promise<number> {
    const start = Date.now();
    await this.health();
    return Date.now() - start;
  }

  /** Get agent status information. */
  async info(): Promise<AgentInfo> {
    const result = await this.get<{ ok: boolean; info: AgentInfo }>('/info');
    return result.info;
  }

  /** Create a new task on the remote agent. */
  async createTask(prompt: string, opts?: CreateTaskOptions): Promise<Task> {
    const body: Record<string, unknown> = { title: prompt };
    if (opts?.model) body.model = opts.model;
    if (opts?.runner) body.runner = opts.runner;
    if (opts?.customCommand) body.customCommand = opts.customCommand;
    if (opts?.speechContext) body.speechContext = opts.speechContext;
    if (opts?.images?.length) body.images = opts.images;

    const result = await this.post<{
      ok: boolean; taskId: string; status: string; runnerId: string; error?: string;
    }>('/tasks', body);

    if (!result.ok) throw new Error(result.error || 'Failed to create task');

    return {
      id: result.taskId,
      title: prompt,
      status: result.status as Task['status'],
      runnerId: result.runnerId,
      createdAt: new Date().toISOString(),
    };
  }

  /** Get task details by ID. */
  async getTask(taskId: string): Promise<Task> {
    const result = await this.get<{ ok: boolean; task: Task }>(`/tasks/${taskId}`);
    return result.task;
  }

  /** List all tasks. */
  async listTasks(): Promise<Task[]> {
    const result = await this.get<{ ok: boolean; tasks: Task[] }>('/tasks');
    return result.tasks;
  }

  /** Stop a running task. */
  async stopTask(taskId: string): Promise<void> {
    const result = await this.post<{ ok: boolean; error?: string }>(`/tasks/${taskId}/stop`);
    if (!result.ok) throw new Error(result.error || 'Failed to stop task');
  }

  /** Delete a task. */
  async deleteTask(taskId: string): Promise<void> {
    await this.del(`/tasks/${taskId}`);
  }

  /** Send a follow-up message to a running task. */
  async continueTask(taskId: string, message: string, images?: ImageAttachment[]): Promise<void> {
    const body: Record<string, unknown> = { input: message };
    if (images?.length) body.images = images;
    const result = await this.post<{ ok: boolean; error?: string }>(
      `/tasks/${taskId}/continue`, body
    );
    if (!result.ok) throw new Error(result.error || 'Failed to continue task');
  }

  /** Clean up old tasks, images, and logs on the agent. */
  async clean(days = 30): Promise<{ tasksRemoved: number; imagesRemoved: number; bytesFreed: number }> {
    const result = await this.post<{ ok: boolean; result: { tasksRemoved: number; imagesRemoved: number; bytesFreed: number } }>(
      '/agent/clean', { days }
    );
    return result.result;
  }

  /**
   * Stream task output. Yields new output chunks as they arrive.
   * @param taskId - Task ID to stream
   * @param pollIntervalMs - Polling interval (default: 500ms)
   */
  async *streamOutput(taskId: string, pollIntervalMs = 500): AsyncGenerator<string> {
    let lastLen = 0;
    while (true) {
      const task = await this.getTask(taskId);
      const output = task.output || '';
      if (output.length > lastLen) {
        yield output.substring(lastLen);
        lastLen = output.length;
      }
      if (task.status === 'completed' || task.status === 'failed' || task.status === 'stopped') {
        return;
      }
      await sleep(pollIntervalMs);
    }
  }

  /** Start a command on the remote agent. */
  async startExec(command: string, opts?: ExecOptions): Promise<{ execId: string; pid: number }> {
    const body: Record<string, unknown> = { command };
    if (opts?.workDir) body.workDir = opts.workDir;
    if (opts?.timeout) body.timeout = opts.timeout;
    if (opts?.env) body.env = opts.env;
    const result = await this.post<{ ok: boolean; execId: string; pid: number; error?: string }>('/exec', body);
    if (!result.ok) throw new Error(result.error || 'Failed to start exec');
    return { execId: result.execId, pid: result.pid };
  }

  /** Get exec session details. */
  async getExec(execId: string): Promise<ExecSession> {
    const result = await this.get<{ ok: boolean; exec: ExecSession }>(`/exec/${execId}`);
    return result.exec;
  }

  /** List all exec sessions. */
  async listExecs(): Promise<ExecSession[]> {
    const result = await this.get<{ ok: boolean; execs: ExecSession[] }>('/exec');
    return result.execs;
  }

  /** Send stdin input to a running exec session. */
  async sendExecInput(execId: string, input: string): Promise<void> {
    await this.post(`/exec/${execId}/input`, { input });
  }

  /** Send a signal to a running exec session. */
  async signalExec(execId: string, signal: string): Promise<void> {
    await this.post(`/exec/${execId}/signal`, { signal });
  }

  /** Kill and remove an exec session. */
  async killExec(execId: string): Promise<void> {
    await this.del(`/exec/${execId}`);
  }

  /** Stream exec output. Yields new stdout/stderr chunks as they arrive. */
  async *streamExecOutput(execId: string, pollIntervalMs = 300): AsyncGenerator<{ type: 'stdout' | 'stderr'; text: string }> {
    let lastStdoutLen = 0;
    let lastStderrLen = 0;
    while (true) {
      const exec = await this.getExec(execId);
      if (exec.stdout.length > lastStdoutLen) {
        yield { type: 'stdout', text: exec.stdout.substring(lastStdoutLen) };
        lastStdoutLen = exec.stdout.length;
      }
      if (exec.stderr.length > lastStderrLen) {
        yield { type: 'stderr', text: exec.stderr.substring(lastStderrLen) };
        lastStderrLen = exec.stderr.length;
      }
      if (exec.status === 'completed' || exec.status === 'failed' || exec.status === 'killed') {
        return;
      }
      await sleep(pollIntervalMs);
    }
  }

  // ── Runners + OAuth (both levels) ────────────────────────────────
  // The heavy lifting (browser OAuth, device-code, install, MCP setup) is
  // done by the Go agent — these just drive it over HTTP.

  /** List installed runners (claude / codex / opencode …) with auth + models. */
  async listRunners(): Promise<{ runners: RunnerInfo[]; default: string | null }> {
    const r = await this.get<{ ok: boolean; runners?: RunnerInfo[]; default?: string }>('/agent/runners');
    return { runners: r.runners ?? [], default: r.default ?? null };
  }

  /** Per-runner auth truth (authoritative for "is this runner logged in"). */
  async runnerAuthStatuses(): Promise<RunnerInfo[]> {
    const r = await this.get<{ ok: boolean; runners?: RunnerInfo[] }>('/runner-auth/status');
    return r.runners ?? [];
  }

  /** Start a runner OAuth (browser/device-code) flow. */
  async runnerAuthStart(runner: string): Promise<RunnerAuthSession> {
    return this.post<RunnerAuthSession>('/runner-auth/browser/start', { runner });
  }

  /** Poll a runner OAuth session. */
  async runnerAuthStatus(sessionId: string): Promise<RunnerAuthSession> {
    return this.get<RunnerAuthSession>(`/runner-auth/browser/status?id=${encodeURIComponent(sessionId)}`);
  }

  /** Submit the pasted OAuth code/token for a runner. */
  async runnerAuthSubmitCode(sessionId: string, code: string): Promise<RunnerAuthSession> {
    return this.post<RunnerAuthSession>(`/runner-auth/browser/submit-code?id=${encodeURIComponent(sessionId)}`, { code });
  }

  /** Cancel an in-flight runner OAuth session. */
  async runnerAuthCancel(sessionId: string): Promise<void> {
    await this.post(`/runner-auth/browser/cancel?id=${encodeURIComponent(sessionId)}`);
  }

  /** Headless/API-key runner setup. setupMCP wires MCP servers into the runner. */
  async runnerAuthSetup(runner: string, opts?: RunnerSetupOptions): Promise<{ ok: boolean; error?: string }> {
    return this.post<{ ok: boolean; error?: string }>('/runner-auth/setup', {
      runner,
      setupMCP: opts?.setupMCP !== false,
      installIfMissing: opts?.installIfMissing === true,
      ...opts,
    });
  }

  /** Yaver account level — is the agent linked to a Yaver account? */
  async accountStatus(): Promise<Record<string, unknown>> {
    return this.get<Record<string, unknown>>('/auth/status');
  }

  /**
   * Start the account-level "Yaver OAuth" link (device-code flow). Requires the
   * caller to be host-authenticated (the owner/agent bearer). The agent kicks
   * off the Convex device-code dance and returns `deviceCodeUrl` + `userCode`
   * to approve in a browser, plus `recovery_id` + `wait_token` to poll with.
   */
  async accountLinkStart(): Promise<AccountLinkSession> {
    return this.post<AccountLinkSession>('/auth/recover', { mode: 'device-code' });
  }

  /** Poll an account-link session until `state === 'recovered'`. */
  async accountLinkStatus(recoveryId: string, waitToken: string): Promise<AccountLinkSession> {
    return this.get<AccountLinkSession>(
      `/auth/recover/session?id=${encodeURIComponent(recoveryId)}&wait_token=${encodeURIComponent(waitToken)}`,
    );
  }

  /**
   * Aggregate readiness snapshot for gating UIs (both OAuth levels + runtime).
   * Best-effort: any failed sub-call degrades gracefully.
   */
  async getCapability(): Promise<YaverCapability> {
    const [health, account, runnersResp, authStatuses] = await Promise.all([
      this.health().then(() => true).catch(() => false),
      this.accountStatus().catch(() => null),
      this.listRunners().catch(() => ({ runners: [] as RunnerInfo[], default: null })),
      this.runnerAuthStatuses().catch(() => [] as RunnerInfo[]),
    ]);
    const authById = new Map(authStatuses.filter((r) => r.id).map((r) => [r.id, r] as const));
    const runners = runnersResp.runners.map((r) => {
      const a = authById.get(r.id);
      return a ? { ...r, authConfigured: a.authConfigured ?? r.authConfigured, ready: a.ready ?? r.ready } : r;
    });
    if (runners.length === 0 && authStatuses.length > 0) runners.push(...authStatuses);
    const accountAuthed = readAccountAuthed(account);
    const anyAuthed = runners.some((r) => r.authConfigured);
    return {
      agentReachable: health,
      account: { authed: accountAuthed, raw: account },
      runners,
      defaultRunner: runnersResp.default || runners.find((r) => r.isDefault)?.id || runners[0]?.id || null,
      ready: health && anyAuthed,
      needs: {
        yaverAccountAuth: health && !accountAuthed,
        runnerAuth: runners.filter((r) => r.installed !== false && !r.authConfigured).map((r) => r.id),
      },
    };
  }

  // ── HTTP helpers ─────────────────────────────────────────────────

  private async get<T>(path: string): Promise<T> {
    const resp = await fetchWithTimeout(`${this.baseURL}${path}`, {
      headers: { Authorization: `Bearer ${this.authToken}` },
    }, this.timeout);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}: ${await resp.text()}`);
    return resp.json();
  }

  private async post<T>(path: string, body?: unknown): Promise<T> {
    const resp = await fetchWithTimeout(`${this.baseURL}${path}`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    }, this.timeout);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}: ${await resp.text()}`);
    return resp.json();
  }

  private async del(path: string): Promise<void> {
    const resp = await fetchWithTimeout(`${this.baseURL}${path}`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${this.authToken}` },
    }, this.timeout);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/** Tolerant read of the agent's /auth/status across versions. */
function readAccountAuthed(account: Record<string, unknown> | null): boolean {
  if (!account) return false;
  if (typeof account.authed === 'boolean') return account.authed;
  if (typeof account.authenticated === 'boolean') return account.authenticated as boolean;
  if (typeof account.loggedIn === 'boolean') return account.loggedIn as boolean;
  if (account.user && typeof account.user === 'object') return true;
  if (typeof account.email === 'string' && account.email) return true;
  if (typeof account.status === 'string') {
    return ['authenticated', 'ok', 'linked', 'ready'].includes((account.status as string).toLowerCase());
  }
  return false;
}

async function fetchWithTimeout(
  url: string,
  init: RequestInit,
  timeoutMs: number
): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}
