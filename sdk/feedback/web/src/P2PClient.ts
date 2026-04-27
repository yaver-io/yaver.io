import type {
  AgentCommand,
  CapabilitySnapshot,
  FeedbackBundle,
  FeedbackProjectActionResult,
  FeedbackChangeSet,
  FeedbackReportSummary,
  FeedbackReviewEntry,
  IncidentEvent,
  OperationState,
  ReloadAck,
  RunnerAuthSetupResult,
  RunnerAuthStatus,
  RunnerBrowserAuthSession,
} from './types';

/**
 * P2PClient — lightweight HTTP client for Yaver agent communication.
 * Used by the feedback SDK to upload reports, start builds, and stream feedback.
 */
export class P2PClient {
  constructor(
    private baseUrl: string,
    private authToken: string = '',
    /**
     * Shared relay secret. When the embedded agentUrl points through the
     * Yaver relay (e.g. `https://public.yaver.io/d/<deviceId>`), the relay
     * rejects unauthenticated requests with 401. Pass the relay password
     * here and it will be attached as `X-Relay-Password` on every
     * request.
     */
    private relayPassword: string = ''
  ) {}

  setBaseUrl(url: string): void {
    this.baseUrl = url.replace(/\/$/, '');
  }

  setAuthToken(token: string): void {
    this.authToken = token;
  }

  setRelayPassword(password: string): void {
    this.relayPassword = password;
  }

  private get headers(): Record<string, string> {
    const h: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.authToken) h['Authorization'] = `Bearer ${this.authToken}`;
    if (this.relayPassword) h['X-Relay-Password'] = this.relayPassword;
    h['X-Client-Platform'] = 'web';
    return h;
  }

  /** Mirror of the shared header block for endpoints that build their own headers. */
  private augmentHeaders(base: Record<string, string>): Record<string, string> {
    if (this.authToken) base['Authorization'] = `Bearer ${this.authToken}`;
    if (this.relayPassword) base['X-Relay-Password'] = this.relayPassword;
    return base;
  }

  /**
   * Start a remote browser-style sign-in for a runner (codex --device-auth,
   * claude auth login --console). Returns a session id + the verification URL
   * + one-time code so carrotbytes.xyz end-users can auth the machine's CLI
   * without SSH or API keys.
   */
  async startRunnerBrowserAuth(runner: string): Promise<RunnerBrowserAuthSession> {
    const resp = await fetch(`${this.baseUrl}/runner-auth/browser/start`, {
      method: 'POST',
      headers: this.augmentHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ runner }),
    });
    if (!resp.ok) throw new Error(`startRunnerBrowserAuth(${runner}) HTTP ${resp.status}`);
    const data = await resp.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async getRunnerBrowserAuthStatus(sessionId: string): Promise<RunnerBrowserAuthSession> {
    const url = new URL(`${this.baseUrl}/runner-auth/browser/status`);
    url.searchParams.set('id', sessionId);
    const resp = await fetch(url.toString(), { headers: this.augmentHeaders({}) });
    if (!resp.ok) throw new Error(`getRunnerBrowserAuthStatus HTTP ${resp.status}`);
    const data = await resp.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async cancelRunnerBrowserAuth(sessionId: string): Promise<void> {
    const url = new URL(`${this.baseUrl}/runner-auth/browser/cancel`);
    url.searchParams.set('id', sessionId);
    await fetch(url.toString(), { method: 'POST', headers: this.augmentHeaders({}) }).catch(() => {});
  }

  async getRunnerAuthStatus(): Promise<RunnerAuthStatus[]> {
    const resp = await fetch(`${this.baseUrl}/runner-auth/status`, {
      method: 'GET',
      headers: this.authHeaders(),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Runner auth status failed (${resp.status}): ${text}`);
    }
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    const rows = Array.isArray((data as any)?.runners) ? ((data as any).runners as Array<Record<string, unknown>>) : [];
    return rows.map((row) => ({
      id: typeof row.id === 'string' ? row.id : typeof row.ID === 'string' ? row.ID : '',
      name: typeof row.name === 'string' ? row.name : typeof row.Name === 'string' ? row.Name : '',
      installed: row.installed === true || row.Installed === true,
      ready: row.ready === true || row.Ready === true,
      authConfigured: row.authConfigured === true || row.AuthConfigured === true,
      authSource:
        typeof row.authSource === 'string'
          ? row.authSource
          : typeof row.AuthSource === 'string'
            ? row.AuthSource
            : undefined,
      warning:
        typeof row.warning === 'string'
          ? row.warning
          : typeof row.Warning === 'string'
            ? row.Warning
            : undefined,
      error:
        typeof row.error === 'string'
          ? row.error
          : typeof row.Error === 'string'
            ? row.Error
            : undefined,
      detail:
        typeof row.detail === 'string'
          ? row.detail
          : typeof row.Detail === 'string'
            ? row.Detail
            : undefined,
    }));
  }

  async setupRunnerAuth(body: {
    runner: string;
    openai_api_key?: string;
    anthropic_api_key?: string;
    anthropic_auth_token?: string;
    claude_code_oauth_token?: string;
    glm_api_key?: string;
    zai_api_key?: string;
    notes?: string;
    install_if_missing?: boolean;
    codex_login?: boolean;
    setup_mcp?: boolean;
  }): Promise<RunnerAuthSetupResult> {
    const resp = await fetch(`${this.baseUrl}/runner-auth/setup`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Runner auth setup failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  async getAvailableRunners(): Promise<Array<{
    id: string;
    name: string;
    installed: boolean;
    ready: boolean;
    authConfigured: boolean;
    authSource?: string;
    warning?: string;
    error?: string;
    isDefault: boolean;
  }>> {
    const resp = await fetch(`${this.baseUrl}/agent/runners`, {
      method: 'GET',
      headers: this.authHeaders(),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Runners failed (${resp.status}): ${text}`);
    }
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    const rows = Array.isArray((data as any)?.runners) ? ((data as any).runners as Array<Record<string, unknown>>) : [];
    return rows.map((row) => ({
      id: typeof row.id === 'string' ? row.id : '',
      name: typeof row.name === 'string' ? row.name : '',
      installed: row.installed === true,
      ready: row.ready === true,
      authConfigured: row.authConfigured === true,
      authSource: typeof row.authSource === 'string' ? row.authSource : undefined,
      warning: typeof row.warning === 'string' ? row.warning : undefined,
      error: typeof row.error === 'string' ? row.error : undefined,
      isDefault: row.isDefault === true,
    }));
  }

  async switchRunner(runnerId: string): Promise<{ ok: boolean; runnerId: string; runner: string }> {
    const resp = await fetch(`${this.baseUrl}/agent/runner/switch`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ runnerId }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Runner switch failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  async capabilitySnapshot(): Promise<CapabilitySnapshot | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/capabilities/snapshot`, { headers: this.headers });
      if (!resp.ok) return null;
      const data = await resp.json().catch(() => ({}));
      return (data?.snapshot ?? null) as CapabilitySnapshot | null;
    } catch {
      return null;
    }
  }

  async incidents(opts: {
    category?: string;
    severity?: string;
    code?: string;
    deviceId?: string;
    projectPath?: string;
    includeResolved?: boolean;
    limit?: number;
  } = {}): Promise<IncidentEvent[]> {
    try {
      const url = new URL(`${this.baseUrl}/incidents`);
      if (opts.category) url.searchParams.set('category', opts.category);
      if (opts.severity) url.searchParams.set('severity', opts.severity);
      if (opts.code) url.searchParams.set('code', opts.code);
      if (opts.deviceId) url.searchParams.set('device', opts.deviceId);
      if (opts.projectPath) url.searchParams.set('projectPath', opts.projectPath);
      if (opts.includeResolved) url.searchParams.set('includeResolved', '1');
      if (typeof opts.limit === 'number') url.searchParams.set('limit', String(opts.limit));
      const resp = await fetch(url.toString(), { headers: this.headers });
      if (!resp.ok) return [];
      const data = await resp.json().catch(() => ({}));
      return Array.isArray(data?.incidents) ? (data.incidents as IncidentEvent[]) : [];
    } catch {
      return [];
    }
  }

  async operations(opts: {
    kind?: string;
    status?: string;
    deviceId?: string;
    projectPath?: string;
    limit?: number;
  } = {}): Promise<OperationState[]> {
    try {
      const url = new URL(`${this.baseUrl}/operations`);
      if (opts.kind) url.searchParams.set('kind', opts.kind);
      if (opts.status) url.searchParams.set('status', opts.status);
      if (opts.deviceId) url.searchParams.set('device', opts.deviceId);
      if (opts.projectPath) url.searchParams.set('projectPath', opts.projectPath);
      if (typeof opts.limit === 'number') url.searchParams.set('limit', String(opts.limit));
      const resp = await fetch(url.toString(), { headers: this.headers });
      if (!resp.ok) return [];
      const data = await resp.json().catch(() => ({}));
      return Array.isArray(data?.operations) ? (data.operations as OperationState[]) : [];
    } catch {
      return [];
    }
  }

  /** Health check — is the agent reachable? */
  async health(): Promise<boolean> {
    try {
      const resp = await fetch(`${this.baseUrl}/health`, { signal: AbortSignal.timeout(2000) });
      return resp.ok;
    } catch {
      return false;
    }
  }

  /** Get agent info (hostname, version, platform). */
  async info(): Promise<{ hostname: string; version: string; platform: string } | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/info`, { headers: this.headers });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  async getDevServerStatus(): Promise<{
    running: boolean;
    framework?: string;
    port?: number;
    workDir?: string;
    bundleUrl?: string;
    directUrl?: string;
  } | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/dev/status`, { headers: this.authHeaders() });
      if (!resp.ok) return null;
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      return {
        running: data.running === true,
        framework: typeof data.framework === 'string' ? data.framework : undefined,
        port: typeof data.port === 'number' ? data.port : undefined,
        workDir: typeof data.workDir === 'string' ? data.workDir : undefined,
        bundleUrl: typeof data.bundleUrl === 'string' ? data.bundleUrl : undefined,
        directUrl: typeof data.directUrl === 'string' ? data.directUrl : undefined,
      };
    } catch {
      return null;
    }
  }

  async recoverAgentAuth(): Promise<boolean> {
    const resp = await fetch(`${this.baseUrl}/auth/recover`, {
      method: 'POST',
      headers: this.augmentHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ mode: 'direct' }),
    });
    return resp.ok;
  }

  /** Upload feedback bundle via multipart POST. Returns report ID. */
  async uploadFeedback(bundle: FeedbackBundle): Promise<FeedbackReportSummary | null> {
    const form = new FormData();
    form.append('metadata', JSON.stringify(bundle.metadata));
    if (bundle.video) form.append('video', bundle.video, 'recording.webm');
    if (bundle.audio) form.append('audio', bundle.audio, 'voice.webm');
    bundle.screenshots.forEach((s, i) => form.append(`screenshot_${i}`, s, `screenshot_${i}.png`));

    const headers: Record<string, string> = this.augmentHeaders({});

    try {
      const resp = await fetch(`${this.baseUrl}/feedback`, { method: 'POST', headers, body: form });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  /** Stream feedback events in live mode. Returns an EventSource-like reader. */
  async streamFeedback(events: Array<{ type: string; text?: string; data?: string }>): Promise<void> {
    const headers: Record<string, string> = this.augmentHeaders({ 'Content-Type': 'application/json' });

    await fetch(`${this.baseUrl}/feedback/stream`, {
      method: 'POST',
      headers,
      body: events.map(e => JSON.stringify(e)).join('\n'),
    });
  }

  /** List builds on the agent. */
  async listBuilds(): Promise<Array<{ id: string; platform: string; status: string; artifactName?: string }>> {
    try {
      const resp = await fetch(`${this.baseUrl}/builds`, { headers: this.headers });
      if (!resp.ok) return [];
      return resp.json();
    } catch {
      return [];
    }
  }

  /** Start a build. */
  async startBuild(platform: string, workDir?: string): Promise<{ id: string } | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/builds`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify({ platform, workDir: workDir || '' }),
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  /** Create a task (voice command → agent acts). */
  async createTask(prompt: string): Promise<{ id: string } | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/tasks`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify({ title: prompt }),
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  /** Create a fix task from feedback. */
  async fixFromFeedback(
    feedbackId: string,
    opts: { mode?: 'candidate' | 'direct'; comment?: string } = {},
  ): Promise<{ taskId: string; changeSet?: FeedbackChangeSet } | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/feedback/${feedbackId}/fix`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify(opts),
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  async getFeedbackChangeSet(feedbackId: string): Promise<FeedbackChangeSet | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/feedback/${feedbackId}/change-set`, {
        headers: this.headers,
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  async updateFeedbackChangeSet(
    feedbackId: string,
    patch: Partial<FeedbackChangeSet>,
  ): Promise<FeedbackChangeSet | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/feedback/${feedbackId}/change-set`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify(patch),
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  async reviewFeedbackChangeSet(
    feedbackId: string,
    review: Pick<FeedbackReviewEntry, 'action' | 'comment' | 'desiredOutcome'>,
  ): Promise<FeedbackChangeSet | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/feedback/${feedbackId}/review`, {
        method: 'POST',
        headers: this.headers,
        body: JSON.stringify(review),
      });
      if (!resp.ok) return null;
      return resp.json();
    } catch {
      return null;
    }
  }

  /** Get artifact download URL. */
  getArtifactUrl(buildId: string): string {
    return `${this.baseUrl}/builds/${buildId}/artifact`;
  }

  async reloadApp(
    mode: 'dev' | 'bundle' = 'dev',
    opts?: { projectName?: string; projectPath?: string; bundleId?: string },
  ): Promise<ReloadAck> {
    if (mode === 'dev') {
      const resp = await fetch(`${this.baseUrl}/dev/reload`, {
        method: 'POST',
        headers: this.authHeaders(),
      });
      if (resp.ok) {
        const payload = await resp.json().catch(() => ({} as Record<string, unknown>));
        return {
          ok: true,
          mode: 'dev',
          acknowledged: true,
          nativeChangesDetected: payload.nativeChangesDetected === true,
          changeClass:
            typeof payload.changeClass === 'string' ? payload.changeClass : undefined,
          message:
            payload.nativeChangesDetected === true
              ? 'Reload accepted, but native changes need a rebuild.'
              : 'Hot reload request accepted.',
        };
      }
    }

    const resp = await fetch(`${this.baseUrl}/dev/reload-app`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        mode: 'bundle',
        projectName: opts?.projectName,
        projectPath: opts?.projectPath,
        bundleId: opts?.bundleId,
      }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Reload failed (${resp.status}): ${text}`);
    }
    const payload = await resp.json().catch(() => ({} as Record<string, unknown>));
    return {
      ok: true,
      mode: 'bundle',
      acknowledged: true,
      nativeChangesDetected: payload.nativeChangesDetected === true,
      changeClass: typeof payload.changeClass === 'string' ? payload.changeClass : undefined,
      message:
        typeof payload.message === 'string' && payload.message.trim()
          ? payload.message
          : 'Reload request acknowledged.',
    };
  }

  async vibing(
    prompt: string,
    opts?: { projectName?: string; projectPath?: string; bundleId?: string },
  ): Promise<{ taskId: string }> {
    const resp = await fetch(`${this.baseUrl}/vibing/execute`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        prompt,
        projectName: opts?.projectName,
        projectPath: opts?.projectPath ?? '',
        bundleId: opts?.bundleId,
      }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Vibing failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  /** Fetch a task by ID — returns the agent's full task record incl.
   *  the growing `output` blob and `status`. The chat surface polls
   *  this every ~1.5 s while the task is alive so each new agent line
   *  paints into the transcript. */
  async getTask(taskId: string): Promise<{
    id: string;
    status: string;
    title?: string;
    output?: string;
    resultText?: string;
  }> {
    const resp = await fetch(`${this.baseUrl}/tasks/${encodeURIComponent(taskId)}`, {
      headers: this.authHeaders(),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] getTask failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  /** Append a follow-up turn to an existing vibing task. Same agent,
   *  same workdir, same conversation — the runner picks up where it
   *  left off. Used by the chat surface for multi-turn vibing instead
   *  of spawning a fresh task on every message. */
  async continueTask(taskId: string, input: string): Promise<{ ok?: boolean }> {
    const resp = await fetch(`${this.baseUrl}/tasks/${encodeURIComponent(taskId)}/continue`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ input }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] continueTask failed (${resp.status}): ${text}`);
    }
    return resp.json().catch(() => ({ ok: true }));
  }

  async getVibingEligibility(
    opts?: { projectName?: string; projectPath?: string; bundleId?: string },
  ): Promise<{
    canVibe: boolean;
    reason?: string;
    guidance?: string;
    projectName?: string;
    projectPath?: string;
    provider?: string;
    repoHost?: string;
    repoFullName?: string;
    runner?: string;
    needsRunnerAuth?: boolean;
    needsGitSetup?: boolean;
  }> {
    const params = new URLSearchParams();
    if (opts?.projectName) params.set('projectName', opts.projectName);
    if (opts?.projectPath) params.set('projectPath', opts.projectPath);
    if (opts?.bundleId) params.set('bundleId', opts.bundleId);
    const query = params.toString();
    const resp = await fetch(
      `${this.baseUrl}/vibing/eligibility${query ? `?${query}` : ''}`,
      {
        method: 'GET',
        headers: this.authHeaders(),
      },
    );
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Vibing eligibility failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  async gitProviderDetect(): Promise<Array<{
    provider: string;
    host: string;
    username: string;
    avatarUrl?: string;
    hasToken: boolean;
  }>> {
    const resp = await fetch(`${this.baseUrl}/git/provider/detect`, {
      method: 'GET',
      headers: this.authHeaders(),
    });
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(`[P2PClient] Git provider detect failed (${resp.status})`);
    }
    const rows = Array.isArray((data as any)?.providers) ? ((data as any).providers as Array<Record<string, unknown>>) : [];
    return rows.map((row) => ({
      provider: typeof row.provider === 'string' ? row.provider : '',
      host: typeof row.host === 'string' ? row.host : '',
      username: typeof row.username === 'string' ? row.username : '',
      avatarUrl: typeof row.avatarUrl === 'string' ? row.avatarUrl : undefined,
      hasToken: row.hasToken === true,
    }));
  }

  async gitProviderSetup(params: {
    provider: 'github' | 'gitlab';
    token: string;
    host?: string;
    generateSsh?: boolean;
  }): Promise<{ ok: boolean; username?: string; host?: string; provider?: string; error?: string }> {
    const resp = await fetch(`${this.baseUrl}/git/provider/setup`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(params),
    });
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(
        typeof data?.error === 'string'
          ? data.error
          : `[P2PClient] Git provider setup failed (${resp.status})`,
      );
    }
    return data as { ok: boolean; username?: string; host?: string; provider?: string; error?: string };
  }

  /**
   * Bind a project name to a canonical git remote URL on the agent. The
   * agent stores the mapping locally and uses it as a fallback when the
   * project on disk has no `git remote` configured (or hasn't been cloned
   * yet). Owner-only — guests and SDK tokens cannot call this.
   */
  async setProjectRemote(params: {
    projectName: string;
    remoteUrl: string;
  }): Promise<{
    ok: boolean;
    project?: {
      name: string;
      remoteUrl: string;
      provider: string;
      host: string;
      repo: string;
      setAt: string;
    };
    error?: string;
  }> {
    const resp = await fetch(`${this.baseUrl}/vibing/project/remote`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(params),
    });
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(
        typeof (data as any)?.error === 'string'
          ? (data as any).error
          : `[P2PClient] Set project remote failed (${resp.status})`,
      );
    }
    return data as Awaited<ReturnType<P2PClient['setProjectRemote']>>;
  }

  /**
   * Look up the registered remote for a project, if any. Returns
   * { found: false, project: null } when no entry exists.
   */
  async getProjectRemote(projectName: string): Promise<{
    ok: boolean;
    found: boolean;
    project: {
      name: string;
      remoteUrl: string;
      provider: string;
      host: string;
      repo: string;
      setAt: string;
    } | null;
  }> {
    const url = new URL(`${this.baseUrl}/vibing/project/remote`);
    url.searchParams.set('projectName', projectName);
    const resp = await fetch(url.toString(), {
      method: 'GET',
      headers: this.authHeaders(),
    });
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(
        typeof (data as any)?.error === 'string'
          ? (data as any).error
          : `[P2PClient] Get project remote failed (${resp.status})`,
      );
    }
    return data as Awaited<ReturnType<P2PClient['getProjectRemote']>>;
  }

  /**
   * Clone a repository onto the agent's machine via the existing
   * `/repos/clone` endpoint. Owner-only.
   */
  async cloneRepo(params: {
    url: string;
    dir?: string;
    branch?: string;
  }): Promise<{
    ok: boolean;
    path?: string;
    name?: string;
    error?: string;
  }> {
    const resp = await fetch(`${this.baseUrl}/repos/clone`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(params),
    });
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(
        typeof (data as any)?.error === 'string'
          ? (data as any).error
          : `[P2PClient] Clone failed (${resp.status})`,
      );
    }
    return data as Awaited<ReturnType<P2PClient['cloneRepo']>>;
  }

  /**
   * Search for an existing clone of `remoteUrl` anywhere under the
   * agent's project-discovery roots. Returns null when no match.
   *
   * The widget calls this BEFORE issuing /repos/clone so a user who
   * already cloned the repo manually (often months ago, in a custom
   * location) doesn't get a duplicate fresh clone. Agent endpoint
   * /git/find-repo (cli 1.99.88+); older agents return 404 and the
   * caller should fall through to a regular clone.
   */
  async findExistingRepo(remoteUrl: string): Promise<{
    path: string;
    remoteUrl: string;
  } | null> {
    const resp = await fetch(`${this.baseUrl}/git/find-repo`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ remoteUrl }),
    });
    if (resp.status === 404) {
      // Old agent without the endpoint — caller treats as "no match"
      // and proceeds to clone. Don't throw; the wizard would otherwise
      // surface a confusing error.
      return null;
    }
    const data = await resp.json().catch(() => ({} as Record<string, unknown>));
    if (!resp.ok) {
      throw new Error(
        typeof (data as any)?.error === 'string'
          ? (data as any).error
          : `[P2PClient] find-repo failed (${resp.status})`,
      );
    }
    const m = (data as any)?.match;
    if (!m || typeof m.path !== 'string' || !m.path) return null;
    return {
      path: m.path,
      remoteUrl: typeof m.remoteUrl === 'string' ? m.remoteUrl : remoteUrl,
    };
  }

  async commitProject(
    opts?: { projectName?: string; projectPath?: string; bundleId?: string; message?: string },
  ): Promise<FeedbackProjectActionResult> {
    const resp = await fetch(`${this.baseUrl}/vibing/commit`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        projectName: opts?.projectName,
        projectPath: opts?.projectPath,
        bundleId: opts?.bundleId,
        message: opts?.message,
      }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Commit failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  async deployProject(
    opts?: { projectName?: string; projectPath?: string; bundleId?: string; target?: string },
  ): Promise<FeedbackProjectActionResult> {
    const resp = await fetch(`${this.baseUrl}/vibing/deploy`, {
      method: 'POST',
      headers: {
        ...this.authHeaders(),
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        projectName: opts?.projectName,
        projectPath: opts?.projectPath,
        bundleId: opts?.bundleId,
        target: opts?.target,
      }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`[P2PClient] Deploy failed (${resp.status}): ${text}`);
    }
    return resp.json();
  }

  async connectCommandStream(
    handler: (command: AgentCommand) => void,
    opts?: { deviceId?: string; platform?: string; appName?: string; signal?: AbortSignal },
  ): Promise<void> {
    const params = new URLSearchParams();
    if (opts?.deviceId) params.set('device', opts.deviceId);
    const url = `${this.baseUrl}/blackbox/command-stream${params.toString() ? `?${params.toString()}` : ''}`;
    const response = await fetch(url, {
      method: 'GET',
      headers: {
        ...this.authHeaders(),
        Accept: 'text/event-stream',
        ...(opts?.deviceId ? { 'X-Device-ID': opts.deviceId } : {}),
        ...(opts?.platform ? { 'X-Platform': opts.platform } : {}),
        ...(opts?.appName ? { 'X-App-Name': opts.appName } : {}),
      },
      signal: opts?.signal,
    });
    if (!response.ok || !response.body) {
      throw new Error(`[P2PClient] Command stream failed (${response.status})`);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue;
        try {
          const payload = JSON.parse(line.slice(6));
          if (payload?.type === 'command' && payload.command) {
            handler(payload.command as AgentCommand);
          }
        } catch {
          // ignore malformed chunks
        }
      }
    }
  }

  private authHeaders(): Record<string, string> {
    const headers: Record<string, string> = {};
    if (this.authToken) headers.Authorization = `Bearer ${this.authToken}`;
    if (this.relayPassword) headers['X-Relay-Password'] = this.relayPassword;
    return headers;
  }
}
