import { Platform } from 'react-native';
import { FeedbackBundle, TestSession, TodoItemDetail, TodoItemSummary, VoiceCapability } from './types';

export interface FeedbackEvent {
  type: string;
  timestamp: string;
  data: any;
}

/**
 * Lightweight P2P HTTP client for communicating with a Yaver agent.
 *
 * Reuses the same endpoint patterns as the main upload module but adds
 * support for streaming feedback, listing builds, and triggering builds.
 */
export class P2PClient {
  private baseUrl: string;
  private authToken: string;

  constructor(baseUrl: string, authToken: string) {
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.authToken = authToken;
  }

  /** Update the base URL (e.g. after re-discovery). */
  setBaseUrl(url: string): void {
    this.baseUrl = url.replace(/\/$/, '');
  }

  /** Update the auth token. */
  setAuthToken(token: string): void {
    this.authToken = token;
  }

  /** Health check — returns true if the agent is reachable. */
  async health(): Promise<boolean> {
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 3000);

      const response = await fetch(`${this.baseUrl}/health`, {
        method: 'GET',
        signal: controller.signal,
      });

      clearTimeout(timeoutId);
      return response.ok;
    } catch {
      return false;
    }
  }

  /** Get agent info (hostname, version, platform). */
  async info(): Promise<{ hostname: string; version: string; platform: string }> {
    const response = await this.request('GET', '/health');
    const data = await response.json();
    return {
      hostname: data.hostname ?? data.name ?? 'Unknown',
      version: data.version ?? 'unknown',
      platform: data.platform ?? 'unknown',
    };
  }

  /**
   * Upload a feedback bundle via multipart POST.
   * @returns The feedback report ID from the agent.
   */
  async uploadFeedback(bundle: FeedbackBundle): Promise<string> {
    const formData = new FormData();

    formData.append('metadata', JSON.stringify(bundle.metadata));

    for (let i = 0; i < bundle.screenshots.length; i++) {
      const path = bundle.screenshots[i];
      formData.append(`screenshot_${i}`, {
        uri: Platform.OS === 'android' ? `file://${path}` : path,
        type: 'image/png',
        name: `screenshot_${i}.png`,
      } as any);
    }

    if (bundle.audio) {
      formData.append('audio', {
        uri: Platform.OS === 'android' ? `file://${bundle.audio}` : bundle.audio,
        type: 'audio/m4a',
        name: 'voice_note.m4a',
      } as any);
    }

    if (bundle.video) {
      formData.append('video', {
        uri: Platform.OS === 'android' ? `file://${bundle.video}` : bundle.video,
        type: 'video/mp4',
        name: 'screen_recording.mp4',
      } as any);
    }

    const response = await fetch(`${this.baseUrl}/feedback`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
      },
      body: formData,
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Upload failed (${response.status}): ${text}`);
    }

    const result = await response.json();
    return result.id ?? result.reportId ?? 'unknown';
  }

  /**
   * Stream feedback events to the agent in live mode.
   * Sends each event as a JSON POST to `/feedback/stream`.
   */
  async streamFeedback(events: AsyncIterable<FeedbackEvent>): Promise<void> {
    for await (const event of events) {
      const response = await fetch(`${this.baseUrl}/feedback/stream`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${this.authToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(event),
      });

      if (!response.ok) {
        const text = await response.text().catch(() => '');
        throw new Error(
          `[P2PClient] Stream event failed (${response.status}): ${text}`,
        );
      }
    }
  }

  /** List available builds from the agent. */
  async listBuilds(): Promise<any[]> {
    const response = await this.request('GET', '/builds');
    const data = await response.json();
    return data.builds ?? data ?? [];
  }

  /** Start a build for the given platform. */
  async startBuild(platform: string): Promise<any> {
    const response = await fetch(`${this.baseUrl}/builds`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ platform }),
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Start build failed (${response.status}): ${text}`);
    }

    return response.json();
  }

  /**
   * Get voice capability info from the agent.
   * voiceInputEnabled is always true — mobile can always record and send audio.
   * s2sProvider/sttProvider indicate whether transcription is available.
   */
  async voiceStatus(): Promise<VoiceCapability> {
    const response = await this.request('GET', '/voice/status');
    const data = await response.json();
    return {
      voiceInputEnabled: data.voiceInputEnabled ?? true,
      s2sProvider: data.s2sProvider ?? undefined,
      s2sReady: data.s2sReady ?? false,
      sttProvider: data.sttProvider ?? undefined,
      sttReady: data.sttReady ?? false,
    };
  }

  /**
   * Send voice audio to the agent for transcription.
   * Works with any configured STT or S2S provider on the agent.
   * If no provider is configured, audio is saved for manual review.
   * @returns Transcribed text (or empty string if no provider available).
   */
  async transcribeVoice(audioUri: string): Promise<{ text: string; provider: string; audioFile?: string }> {
    const formData = new FormData();
    formData.append('audio', {
      uri: Platform.OS === 'android' ? `file://${audioUri}` : audioUri,
      type: 'audio/wav',
      name: 'voice_input.wav',
    } as any);

    const response = await fetch(`${this.baseUrl}/voice/transcribe`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
      },
      body: formData,
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Voice transcribe failed (${response.status}): ${text}`);
    }

    const result = await response.json();
    return {
      text: result.text ?? '',
      provider: result.provider ?? 'none',
      audioFile: result.audioFile,
    };
  }

  /** Get the download URL for a build artifact. */
  getArtifactUrl(buildId: string): string {
    return `${this.baseUrl}/builds/${buildId}/artifact`;
  }

  /**
   * Start an autonomous test session.
   * The agent reads the codebase for context, then navigates the app
   * on the connected device/emulator, catches exceptions via BlackBox,
   * writes fixes, and hot reloads — all without committing.
   */
  async startTestSession(): Promise<{ sessionId: string }> {
    const response = await fetch(`${this.baseUrl}/test-app/start`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ source: 'feedback-sdk' }),
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Start test session failed (${response.status}): ${text}`);
    }

    return response.json();
  }

  /** Stop a running test session. */
  async stopTestSession(): Promise<void> {
    await fetch(`${this.baseUrl}/test-app/stop`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.authToken}` },
    });
  }

  /** Get the current test session status and list of fixes. */
  async getTestSession(): Promise<TestSession> {
    const response = await this.request('GET', '/test-app/status');
    return response.json();
  }

  // ─── Todo List (queued bug reports) ───

  /** Add an item to the todo queue. Returns the new item ID and total pending count. */
  async addTodoItem(bundle: FeedbackBundle): Promise<{ id: string; count: number }> {
    const formData = new FormData();

    const metadata = {
      description: bundle.metadata.userNote || 'Bug report',
      source: 'sdk',
      deviceInfo: {
        platform: bundle.metadata.device.platform,
        model: bundle.metadata.device.model,
        osVersion: bundle.metadata.device.osVersion,
      },
      errors: bundle.errors || [],
    };
    formData.append('metadata', JSON.stringify(metadata));

    for (let i = 0; i < bundle.screenshots.length; i++) {
      const path = bundle.screenshots[i];
      formData.append(`screenshot_${i}`, {
        uri: Platform.OS === 'android' ? `file://${path}` : path,
        type: 'image/png',
        name: `screenshot_${i}.png`,
      } as any);
    }

    if (bundle.audio) {
      formData.append('audio', {
        uri: Platform.OS === 'android' ? `file://${bundle.audio}` : bundle.audio,
        type: 'audio/m4a',
        name: 'voice_note.m4a',
      } as any);
    }

    const response = await fetch(`${this.baseUrl}/todolist`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.authToken}` },
      body: formData,
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Add todo failed (${response.status}): ${text}`);
    }

    return response.json();
  }

  /** Get the count of pending todo items (for badge display). */
  async todoCount(): Promise<number> {
    try {
      const response = await this.request('GET', '/todolist/count');
      const data = await response.json();
      return data.count ?? 0;
    } catch {
      return 0;
    }
  }

  /** List all todo items with summary info. */
  async listTodoItems(): Promise<TodoItemSummary[]> {
    const response = await this.request('GET', '/todolist');
    const data = await response.json();
    return data.items ?? [];
  }

  /** Get full detail for a single todo item. */
  async getTodoItem(id: string): Promise<TodoItemDetail> {
    const response = await this.request('GET', `/todolist/${id}`);
    return response.json();
  }

  /** Remove a todo item from the queue. */
  async removeTodoItem(id: string): Promise<void> {
    await fetch(`${this.baseUrl}/todolist/${id}`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${this.authToken}` },
    });
  }

  /** Implement all pending items as a single batch task. Returns the task ID. */
  async implementAllTodos(): Promise<{ taskId: string; itemCount: number }> {
    const response = await fetch(`${this.baseUrl}/todolist/implement-all`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        'Content-Type': 'application/json',
      },
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Implement all failed (${response.status}): ${text}`);
    }

    return response.json();
  }

  /** Toggle auto-consume mode: items are implemented immediately as they arrive. */
  async setAutoConsume(enabled: boolean): Promise<void> {
    await fetch(`${this.baseUrl}/todolist/auto-consume`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ enabled }),
    });
  }

  /** Check if auto-consume is enabled. */
  async getAutoConsume(): Promise<boolean> {
    try {
      const response = await this.request('GET', '/todolist/auto-consume');
      const data = await response.json();
      return data.enabled ?? false;
    } catch {
      return false;
    }
  }

  /** Get task output/logs for a specific task (linked from a todo item). */
  async getTaskOutput(taskId: string): Promise<{ output: string; status: string }> {
    try {
      const response = await this.request('GET', `/tasks/${taskId}`);
      const data = await response.json();
      return { output: data.output ?? '', status: data.status ?? 'unknown' };
    } catch {
      return { output: '', status: 'unknown' };
    }
  }

  /**
   * Smart chat: send a message and let the agent auto-classify it.
   * The agent determines if it's a todo item, continuation, or immediate action
   * and acts on it automatically. No user interaction needed.
   */
  async smartChat(message: string, deviceInfo?: { platform: string; model: string; osVersion: string }): Promise<{
    intent: 'todo' | 'action' | 'continuation';
    todoItemId?: string;
    taskId?: string;
    todoCount?: number;
    project?: string;
    acted: boolean;
  }> {
    const response = await fetch(`${this.baseUrl}/todolist/classify`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.authToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        message,
        source: 'sdk',
        autoAct: true,
        deviceInfo,
      }),
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Smart chat failed (${response.status}): ${text}`);
    }

    return response.json();
  }

  /**
   * Get full agent info including project metadata, dev server status,
   * todo stats, and task stats.
   */
  async agentInfo(): Promise<{
    hostname: string;
    version: string;
    project: { name: string; path: string; gitBranch?: string; framework?: string };
    devServer?: { running: boolean; framework?: string; port?: number };
    todoCount: number;
    todoTotal: number;
    todoDone: number;
    autoConsume: boolean;
    taskStats: { total: number; done: number; running: number; failed: number };
  }> {
    const response = await this.request('GET', '/info');
    return response.json();
  }

  /** Internal helper for authenticated GET/POST requests. */
  private async request(method: string, path: string): Promise<Response> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${this.authToken}`,
      },
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(
        `[P2PClient] ${method} ${path} failed (${response.status}): ${text}`,
      );
    }

    return response;
  }
}
