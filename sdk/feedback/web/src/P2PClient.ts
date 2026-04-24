import type {
  AgentCommand,
  FeedbackBundle,
  FeedbackChangeSet,
  FeedbackReportSummary,
  FeedbackReviewEntry,
  ReloadAck,
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

  async getVibingEligibility(
    opts?: { projectName?: string; projectPath?: string; bundleId?: string },
  ): Promise<{
    canVibe: boolean;
    reason?: string;
    guidance?: string;
    projectName?: string;
    projectPath?: string;
    provider?: string;
    repoFullName?: string;
    runner?: string;
    needsRunnerAuth?: boolean;
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
