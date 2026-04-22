import type {
  FeedbackBundle,
  FeedbackChangeSet,
  FeedbackReportSummary,
  FeedbackReviewEntry,
} from './types';

/**
 * P2PClient — lightweight HTTP client for Yaver agent communication.
 * Used by the feedback SDK to upload reports, start builds, and stream feedback.
 */
export class P2PClient {
  constructor(
    private baseUrl: string,
    private authToken: string = ''
  ) {}

  private get headers(): Record<string, string> {
    const h: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.authToken) h['Authorization'] = `Bearer ${this.authToken}`;
    h['X-Client-Platform'] = 'web';
    return h;
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

  /** Upload feedback bundle via multipart POST. Returns report ID. */
  async uploadFeedback(bundle: FeedbackBundle): Promise<FeedbackReportSummary | null> {
    const form = new FormData();
    form.append('metadata', JSON.stringify(bundle.metadata));
    if (bundle.video) form.append('video', bundle.video, 'recording.webm');
    if (bundle.audio) form.append('audio', bundle.audio, 'voice.webm');
    bundle.screenshots.forEach((s, i) => form.append(`screenshot_${i}`, s, `screenshot_${i}.png`));

    const headers: Record<string, string> = {};
    if (this.authToken) headers['Authorization'] = `Bearer ${this.authToken}`;

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
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.authToken) headers['Authorization'] = `Bearer ${this.authToken}`;

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
}
