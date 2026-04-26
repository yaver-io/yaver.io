import type {
  AgentCommand,
  FeedbackBundle,
  FeedbackConfig,
  FeedbackProjectActionResult,
  FeedbackReportSummary,
  FeedbackStatusUpdate,
  DeviceInfo,
  ReloadAck,
  RunnerAuthSetupResult,
  RunnerAuthStatus,
  TimelineEvent,
} from './types';
import { YaverDiscovery } from './discovery';
import {
  configureAuthEndpoints,
  DEFAULT_CONVEX_SITE_URL,
  getSelectedDeviceId,
  clearSelectedDeviceId,
  getToken as getCachedToken,
  getUser as getCachedUser,
  saveUser as saveCachedUser,
  validateToken,
  clearToken,
  listReachableDevices,
  saveSelectedDeviceId,
  type RemoteDevice,
} from './auth';
import { openLoginModal } from './LoginModal';
import { openDevicePickerModal } from './DevicePickerModal';
import { P2PClient } from './P2PClient';

/** Escape untrusted text before interpolating into innerHTML strings. */
function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function supportsRunnerBrowserAuth(runner: string): boolean {
  return runner === 'codex' || runner === 'claude';
}

/**
 * YaverFeedback — visual feedback SDK for web apps.
 *
 * Embed in your web frontend during development. Record screen + voice,
 * take screenshots, send bug reports to your Yaver dev machine agent.
 * The AI agent receives the report and fixes the bugs.
 *
 * @example
 * ```ts
 * import { YaverFeedback } from '@yaver/feedback-web';
 *
 * if (process.env.NODE_ENV === 'development') {
 *   YaverFeedback.init({ trigger: 'floating-button' });
 * }
 * ```
 */
export class YaverFeedback {
  private static config: FeedbackConfig | null = null;
  private static mediaRecorder: MediaRecorder | null = null;
  private static audioRecorder: MediaRecorder | null = null;
  private static chunks: Blob[] = [];
  private static audioChunks: Blob[] = [];
  private static screenshots: Blob[] = [];
  private static timeline: TimelineEvent[] = [];
  private static startTime = 0;
  private static recording = false;
  private static consoleErrors: string[] = [];
  private static widget: HTMLElement | null = null;
  private static lastUploadResult: FeedbackReportSummary | null = null;
  private static client: P2PClient | null = null;
  private static commandAbortController: AbortController | null = null;
  private static commandReconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private static deviceId: string | null = null;
  private static reportStyleInjected = false;

  /** Initialize the feedback SDK. Call once in your app entry point. */
  static async init(config: FeedbackConfig = {}): Promise<void> {
    // Default to enabled in development
    if (config.enabled === undefined) {
      config.enabled = YaverFeedback.detectDevEnvironment();
    }

    if (!config.enabled) {
      YaverFeedback.config = { ...config };
      return;
    }

    configureAuthEndpoints({
      convexSiteUrl: config.authConvexSiteUrl,
      webBaseUrl: config.authWebBaseUrl,
    });
    if (!config.convexUrl) {
      config.convexUrl = config.authConvexSiteUrl ?? DEFAULT_CONVEX_SITE_URL;
    }

    // Hydrate auth token from localStorage if caller didn't pass one.
    if (!config.authToken) {
      const cached = getCachedToken();
      if (cached) config.authToken = cached;
    }
    if (!config.preferredDeviceId) {
      const selected = getSelectedDeviceId();
      if (selected) config.preferredDeviceId = selected;
    }

    // Auto-discover agent if no URL provided
    if (!config.agentUrl) {
      const agent = await YaverDiscovery.discover({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (agent) {
        config.agentUrl = agent.url;
        if (agent.relayPassword) {
          config.relayPassword = agent.relayPassword;
        }
        console.log(`[yaver-feedback] Connected to ${agent.hostname} (${agent.url})`);
      } else {
        console.warn('[yaver-feedback] No Yaver agent found. Set agentUrl manually or run "yaver serve" on your dev machine.');
      }
    }

    YaverFeedback.config = config;
    YaverFeedback.client = config.agentUrl
      ? new P2PClient(
          config.agentUrl,
          config.authToken ?? '',
          config.relayPassword ?? '',
        )
      : null;

    // Set up trigger
    if (config.trigger === 'floating-button') {
      YaverFeedback.createFloatingButton(config.buttonPosition || 'bottom-right');
    } else if (config.trigger === 'keyboard') {
      const shortcut = config.shortcut || 'ctrl+shift+f';
      YaverFeedback.setupKeyboardShortcut(shortcut);
    }

    // Capture console errors
    const origError = console.error;
    console.error = (...args: unknown[]) => {
      YaverFeedback.consoleErrors.push(args.map(String).join(' '));
      origError.apply(console, args);
    };

    // Capture unhandled errors
    window.addEventListener('error', (e) => {
      YaverFeedback.consoleErrors.push(`${e.message} at ${e.filename}:${e.lineno}`);
    });

    YaverFeedback.connectCommandStream();
  }

  /** Check if SDK is initialized and enabled. */
  static get isInitialized(): boolean {
    return YaverFeedback.config !== null && YaverFeedback.config.enabled !== false;
  }

  /**
   * Heuristic for "is this a development environment?" used when the host app
   * doesn't pass `enabled` explicitly. The SDK should light up for local work
   * (localhost, loopback, LAN IPs, `.local` mDNS) and stay dark on real
   * production hostnames, regardless of whether the string contains "prod".
   */
  private static detectDevEnvironment(): boolean {
    // process exists in Node toolchains (Vite/Webpack/Next inline it via
    // process.env.NODE_ENV at build time). Bypass TS's missing-Node-types
    // check rather than dragging @types/node into a browser-only SDK.
    const proc = (globalThis as { process?: { env?: { NODE_ENV?: string } } }).process;
    if (proc?.env?.NODE_ENV) {
      return proc.env.NODE_ENV === 'development';
    }
    if (typeof window === 'undefined' || !window.location) return false;
    const host = window.location.hostname;
    if (!host) return false;
    if (host === 'localhost' || host === '0.0.0.0') return true;
    if (host.endsWith('.localhost') || host.endsWith('.local')) return true;
    // IPv4 literal (any dotted-quad — covers loopback, RFC1918, link-local).
    if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host)) return true;
    // IPv6 literal in URL form (browsers bracket these).
    if (host.startsWith('[') && host.endsWith(']')) return true;
    return false;
  }

  /** Start recording screen + microphone. */
  static async startRecording(): Promise<void> {
    if (YaverFeedback.recording) return;

    try {
      // Request screen + audio capture
      const stream = await navigator.mediaDevices.getDisplayMedia({
        video: { width: 1280, height: 720, frameRate: 30 },
        audio: true,
      });

      // Record screen
      YaverFeedback.chunks = [];
      YaverFeedback.mediaRecorder = new MediaRecorder(stream, {
        mimeType: 'video/webm;codecs=vp9',
      });
      YaverFeedback.mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) YaverFeedback.chunks.push(e.data);
      };
      YaverFeedback.mediaRecorder.start(1000); // chunk every 1s

      // Record microphone separately for voice annotations
      try {
        const audioStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        YaverFeedback.audioChunks = [];
        YaverFeedback.audioRecorder = new MediaRecorder(audioStream, {
          mimeType: 'audio/webm;codecs=opus',
        });
        YaverFeedback.audioRecorder.ondataavailable = (e) => {
          if (e.data.size > 0) YaverFeedback.audioChunks.push(e.data);
        };
        YaverFeedback.audioRecorder.start(1000);
      } catch {
        console.warn('[yaver-feedback] Microphone not available');
      }

      YaverFeedback.recording = true;
      YaverFeedback.startTime = Date.now();
      YaverFeedback.screenshots = [];
      YaverFeedback.timeline = [];
      YaverFeedback.consoleErrors = [];
    } catch (err) {
      console.error('[yaver-feedback] Screen recording failed:', err);
    }
  }

  /** Take a screenshot with optional annotation. */
  static captureScreenshot(annotation?: string): void {
    const elapsed = (Date.now() - YaverFeedback.startTime) / 1000;

    // Use html2canvas-style capture via canvas
    // For simplicity, capture via video frame if recording, else use DOM
    YaverFeedback.timeline.push({
      time: elapsed,
      type: 'screenshot',
      text: annotation || `Screenshot at ${elapsed.toFixed(1)}s`,
    });

    if (annotation) {
      YaverFeedback.timeline.push({
        time: elapsed,
        type: 'annotation',
        text: annotation,
      });
    }
  }

  /** Add a voice annotation at the current timestamp. */
  static addAnnotation(text: string): void {
    const elapsed = (Date.now() - YaverFeedback.startTime) / 1000;
    YaverFeedback.timeline.push({
      time: elapsed,
      type: 'voice',
      text,
    });
  }

  /** Stop recording and upload feedback to Yaver agent. */
  static async stopAndSend(): Promise<string | null> {
    if (!YaverFeedback.recording) return null;
    YaverFeedback.recording = false;

    // Stop recorders
    const videoPromise = new Promise<Blob>((resolve) => {
      if (YaverFeedback.mediaRecorder) {
        YaverFeedback.mediaRecorder.onstop = () => {
          resolve(new Blob(YaverFeedback.chunks, { type: 'video/webm' }));
        };
        YaverFeedback.mediaRecorder.stop();
        YaverFeedback.mediaRecorder.stream.getTracks().forEach((t) => t.stop());
      } else {
        resolve(new Blob());
      }
    });

    const audioPromise = new Promise<Blob>((resolve) => {
      if (YaverFeedback.audioRecorder) {
        YaverFeedback.audioRecorder.onstop = () => {
          resolve(new Blob(YaverFeedback.audioChunks, { type: 'audio/webm' }));
        };
        YaverFeedback.audioRecorder.stop();
        YaverFeedback.audioRecorder.stream.getTracks().forEach((t) => t.stop());
      } else {
        resolve(new Blob());
      }
    });

    const [video, audio] = await Promise.all([videoPromise, audioPromise]);

    // Add console errors to timeline
    for (const err of YaverFeedback.consoleErrors) {
      YaverFeedback.timeline.push({
        time: 0,
        type: 'console-error',
        text: err,
      });
    }

    // Build device info
    const deviceInfo: DeviceInfo = {
      platform: 'web',
      browser: navigator.userAgent.includes('Chrome') ? 'Chrome'
        : navigator.userAgent.includes('Firefox') ? 'Firefox'
        : navigator.userAgent.includes('Safari') ? 'Safari' : 'Unknown',
      browserVersion: navigator.appVersion,
      os: navigator.platform,
      screenSize: `${window.innerWidth}x${window.innerHeight}`,
      userAgent: navigator.userAgent,
    };

    const bundle: FeedbackBundle = {
      metadata: {
        source: 'in-app-sdk',
        deviceInfo,
        url: window.location.href,
        timeline: YaverFeedback.timeline,
        consoleErrors: YaverFeedback.consoleErrors,
        project: {
          appName: YaverFeedback.config?.appName,
          projectName: YaverFeedback.config?.projectName,
          projectPath: YaverFeedback.config?.projectPath,
          surface: YaverFeedback.config?.surface,
          releaseChannel: YaverFeedback.config?.releaseChannel,
        },
        candidate: YaverFeedback.config?.candidate
          ? {
              enabled: YaverFeedback.config.candidate.enabled,
              label: YaverFeedback.config.candidate.label,
              baseBranch: YaverFeedback.config.candidate.baseBranch,
              targetBranch: YaverFeedback.config.candidate.targetBranch,
              previewUrl: YaverFeedback.config.candidate.previewUrl,
            }
          : undefined,
      },
      video: video.size > 0 ? video : undefined,
      audio: audio.size > 0 ? audio : undefined,
      screenshots: [...YaverFeedback.screenshots],
    };

    return YaverFeedback.upload(bundle);
  }

  /** Upload feedback bundle to Yaver agent via multipart POST. */
  static async upload(bundle: FeedbackBundle): Promise<string | null> {
    const ready = await YaverFeedback.ensureAgentConnection();
    if (!ready || !YaverFeedback.config?.agentUrl) {
      console.error('[yaver-feedback] No agent URL configured');
      return null;
    }
    const agentUrl = YaverFeedback.config.agentUrl;

    // Lazy auth: prompt the user to sign in if we don't have a token yet.
    // No-op when caller passed `authToken` or `autoLogin: false`.
    const authed = await YaverFeedback.ensureAuthToken();
    if (!authed) {
      console.warn('[yaver-feedback] Sign-in cancelled — bundle not uploaded.');
      return null;
    }

    const form = new FormData();
    form.append('metadata', JSON.stringify(bundle.metadata));

    if (bundle.video) {
      form.append('video', bundle.video, 'recording.webm');
    }
    if (bundle.audio) {
      form.append('audio', bundle.audio, 'voice.webm');
    }
    for (let i = 0; i < bundle.screenshots.length; i++) {
      form.append(`screenshot_${i}`, bundle.screenshots[i], `screenshot_${i}.png`);
    }

    try {
      const headers: Record<string, string> = {};
      if (YaverFeedback.config?.authToken) {
        headers['Authorization'] = `Bearer ${YaverFeedback.config.authToken}`;
      }
      if (YaverFeedback.config?.relayPassword) {
        headers['X-Relay-Password'] = YaverFeedback.config.relayPassword;
      }

      const resp = await fetch(`${agentUrl}/feedback`, {
        method: 'POST',
        headers,
        body: form,
      });

      if (!resp.ok) {
        console.error('[yaver-feedback] Upload failed:', resp.status);
        return null;
      }

      const result = (await resp.json()) as FeedbackReportSummary;
      YaverFeedback.lastUploadResult = result;
      console.log(`[yaver-feedback] Report sent: ${result.id}`);
      if (YaverFeedback.config?.autoFixOnSend && result.id) {
        const fixResp = await fetch(`${agentUrl}/feedback/${result.id}/fix`, {
          method: 'POST',
          headers: {
            ...(YaverFeedback.config?.authToken
              ? { Authorization: `Bearer ${YaverFeedback.config.authToken}` }
              : {}),
            ...(YaverFeedback.config?.relayPassword
              ? { 'X-Relay-Password': YaverFeedback.config.relayPassword }
              : {}),
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({ mode: 'candidate' }),
        });
        if (fixResp.ok) {
          const fix = await fixResp.json();
          if (result.changeSet && fix?.changeSet) result.changeSet = fix.changeSet;
          console.log(`[yaver-feedback] Candidate fix queued: ${fix?.taskId ?? 'unknown task'}`);
        }
      }
      await YaverFeedback.config?.onReportSent?.(result);
      return result.id;
    } catch (err) {
      console.error('[yaver-feedback] Upload error:', err);
      return null;
    }
  }

  /**
   * Ensure we have an auth token. If `autoLogin` is enabled (default) and
   * none is cached, opens the in-app sign-in modal. Returns true if a token
   * is now available; false if the user cancelled or auth is disabled.
   */
  static async ensureAuthToken(): Promise<boolean> {
    if (YaverFeedback.config?.authToken) return true;
    const cached = getCachedToken();
    if (cached) {
      if (YaverFeedback.config) YaverFeedback.config.authToken = cached;
      return true;
    }
    if (YaverFeedback.config?.autoLogin === false) return false;
    try {
      const token = await openLoginModal();
      if (YaverFeedback.config) {
        YaverFeedback.config.authToken = token;
      }
      YaverFeedback.syncClient();
      YaverFeedback.connectCommandStream();
      return true;
    } catch {
      return false;
    }
  }

  static async reloadApp(mode: 'dev' | 'bundle' = 'dev'): Promise<ReloadAck> {
    const client = await YaverFeedback.getClient();
    return client.reloadApp(mode, YaverFeedback.projectIdentity());
  }

  static async commitProject(message?: string): Promise<FeedbackProjectActionResult> {
    const client = await YaverFeedback.getClient();
    return client.commitProject({
      ...YaverFeedback.projectIdentity(),
      message,
    });
  }

  static async deployProject(target?: string): Promise<FeedbackProjectActionResult> {
    const client = await YaverFeedback.getClient();
    return client.deployProject({
      ...YaverFeedback.projectIdentity(),
      target,
    });
  }

  /**
   * Sign out — clears the cached Yaver session token and selected device.
   * After this returns, ensureAuthToken() will prompt for sign-in again on
   * the next interactive flow (record / screenshot / vibing / reload).
   */
  static async signOut(): Promise<void> {
    clearToken();
    clearSelectedDeviceId();
    if (YaverFeedback.config) {
      YaverFeedback.config.authToken = undefined;
      YaverFeedback.config.preferredDeviceId = undefined;
      YaverFeedback.config.agentUrl = undefined;
    }
    YaverFeedback.client = null;
    if (YaverFeedback.commandAbortController) {
      YaverFeedback.commandAbortController.abort();
      YaverFeedback.commandAbortController = null;
    }
    if (YaverFeedback.commandReconnectTimer) {
      clearTimeout(YaverFeedback.commandReconnectTimer);
      YaverFeedback.commandReconnectTimer = null;
    }
  }

  static async getVibingEligibility(): Promise<{
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
    const client = await YaverFeedback.getClient();
    return client.getVibingEligibility(YaverFeedback.projectIdentity());
  }

  static async vibing(prompt: string): Promise<{ taskId: string }> {
    const client = await YaverFeedback.getClient();
    return client.vibing(prompt, YaverFeedback.projectIdentity());
  }

  static async getRunnerAuthStatus(): Promise<RunnerAuthStatus[]> {
    const client = await YaverFeedback.getClient();
    return client.getRunnerAuthStatus();
  }

  static async getAvailableRunners(): Promise<Array<{
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
    const client = await YaverFeedback.getClient();
    return client.getAvailableRunners();
  }

  static async switchRunner(runnerId: string): Promise<{ ok: boolean; runnerId: string; runner: string }> {
    const client = await YaverFeedback.getClient();
    return client.switchRunner(runnerId);
  }

  static async setupRunnerAuth(runner: string): Promise<RunnerAuthSetupResult | null> {
    const client = await YaverFeedback.getClient();
    const normalized = runner.trim().toLowerCase();
    if (normalized === 'opencode') {
      const provider = (prompt('OpenCode provider: openai, anthropic, glm, or zai', 'openai') || '')
        .trim()
        .toLowerCase();
      if (!provider) return null;
      const token = prompt(`Enter the ${provider.toUpperCase()} API key for OpenCode:`) || '';
      if (!token.trim()) return null;
      return client.setupRunnerAuth({
        runner: 'opencode',
        openai_api_key: provider === 'openai' ? token.trim() : undefined,
        anthropic_api_key: provider === 'anthropic' ? token.trim() : undefined,
        glm_api_key: provider === 'glm' ? token.trim() : undefined,
        zai_api_key: provider === 'zai' ? token.trim() : undefined,
      });
    }
    if (normalized === 'codex') {
      const token = prompt('Enter the OpenAI API key for Codex:') || '';
      if (!token.trim()) return null;
      return client.setupRunnerAuth({
        runner: 'codex',
        openai_api_key: token.trim(),
      });
    }
    if (normalized === 'claude') {
      const token = prompt('Enter the Anthropic API key for Claude Code:') || '';
      if (!token.trim()) return null;
      return client.setupRunnerAuth({
        runner: 'claude',
        anthropic_api_key: token.trim(),
      });
    }
    throw new Error(`Unsupported runner: ${runner}`);
  }

  /**
   * Remote sign-in for the selected machine's Codex / Claude CLI.
   * Opens a small overlay showing the one-time URL + code the user enters
   * in their browser; the feedback SDK polls the agent until auth lands.
   *
   * This is the same flow the yaver.io dashboard exposes — embedded here
   * so carrotbytes.xyz end-users never need to leave the page to fix an
   * unauthenticated agent. Zero API keys, zero SSH.
   */
  static async signInRunner(runner: string): Promise<void> {
    const client = await YaverFeedback.getClient();
    const overlay = document.createElement('div');
    overlay.id = 'yaver-fb-runner-auth';
    overlay.style.cssText =
      'position:fixed;inset:0;z-index:99999;display:flex;align-items:center;justify-content:center;background:rgba(2,6,23,0.7);backdrop-filter:blur(6px);padding:16px;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;';
    overlay.innerHTML = `
      <div style="width:min(420px,100%);background:#0f172a;color:#e2e8f0;border-radius:14px;border:1px solid rgba(148,163,184,0.18);padding:18px;box-shadow:0 24px 60px rgba(2,6,23,0.55);">
        <div style="display:flex;justify-content:space-between;align-items:start;margin-bottom:12px;">
          <div>
            <div style="font-size:15px;font-weight:600;">Sign in to ${escapeHtml(runner)} on the remote machine</div>
            <div style="font-size:11px;color:#94a3b8;margin-top:2px;">We'll open a one-time code — enter it in any browser tab.</div>
          </div>
          <button id="yvr-runner-auth-close" style="background:none;border:none;color:#94a3b8;font-size:20px;cursor:pointer;">×</button>
        </div>
        <div id="yvr-runner-auth-body" style="font-size:12px;color:#94a3b8;">Starting sign-in on the remote agent…</div>
      </div>
    `;
    document.body.appendChild(overlay);
    const body = overlay.querySelector<HTMLDivElement>('#yvr-runner-auth-body')!;
    const closeBtn = overlay.querySelector<HTMLButtonElement>('#yvr-runner-auth-close')!;
    closeBtn.onclick = () => overlay.remove();

    let sessionId = '';
    let done = false;
    try {
      const sess = await client.startRunnerBrowserAuth(runner);
      sessionId = sess.id;
      const render = (s: typeof sess) => {
        if (s.status === 'completed') {
          done = true;
          body.innerHTML = `<div style="padding:10px 12px;border:1px solid rgba(34,197,94,0.35);background:rgba(34,197,94,0.1);border-radius:10px;color:#4ade80;">✓ Signed in — ${escapeHtml(s.detail || 'auth saved on the remote machine')}</div>`;
          setTimeout(() => overlay.remove(), 2500);
          return;
        }
        if (s.status === 'failed' || s.status === 'cancelled') {
          done = true;
          body.innerHTML = `<div style="padding:10px 12px;border:1px solid rgba(248,113,113,0.35);background:rgba(248,113,113,0.1);border-radius:10px;color:#fca5a5;">${escapeHtml(s.status === 'cancelled' ? 'Cancelled' : 'Failed')}: ${escapeHtml(s.error || s.detail || 'The CLI exited before sign-in completed.')}</div>`;
          return;
        }
        const urlPart = s.openUrl
          ? `<a href="${escapeHtml(s.openUrl)}" target="_blank" rel="noopener noreferrer" style="display:block;margin-bottom:10px;padding:10px;border-radius:10px;border:1px solid rgba(99,102,241,0.35);background:rgba(99,102,241,0.1);color:#c7d2fe;text-decoration:none;word-break:break-all;">↗ ${escapeHtml(s.openUrl)}</a>`
          : `<div style="padding:10px;border-radius:10px;border:1px solid rgba(148,163,184,0.2);background:rgba(15,23,42,0.6);color:#94a3b8;">Waiting for verification URL from the remote CLI…</div>`;
        const codePart = s.code
          ? `<div style="margin-top:10px;"><div style="font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.08em;color:#94a3b8;margin-bottom:4px;">Enter this code</div><div style="padding:14px;text-align:center;border-radius:10px;border:1px solid rgba(148,163,184,0.22);background:rgba(15,23,42,0.8);font-family:ui-monospace,monospace;font-size:20px;letter-spacing:0.25em;color:#f1f5f9;">${escapeHtml(s.code)}</div></div>`
          : '';
        body.innerHTML = `${urlPart}${codePart}<p style="margin-top:12px;font-size:10px;color:#475569;">Device codes are a common phishing target. Never share this code. This popup closes automatically once sign-in completes.</p>`;
      };
      render(sess);
      const iv = setInterval(async () => {
        if (done) { clearInterval(iv); return; }
        try { render(await client.getRunnerBrowserAuthStatus(sessionId)); } catch { /* transient — keep polling */ }
      }, 1500);
      (overlay as any).__ivHandle = iv;
      closeBtn.onclick = () => {
        if (!done && sessionId) { void client.cancelRunnerBrowserAuth(sessionId); }
        clearInterval(iv);
        overlay.remove();
      };
    } catch (err) {
      body.innerHTML = `<div style="padding:10px 12px;border:1px solid rgba(248,113,113,0.35);background:rgba(248,113,113,0.1);border-radius:10px;color:#fca5a5;">Couldn't start sign-in: ${escapeHtml(err instanceof Error ? err.message : String(err))}</div>`;
    }
  }

  /** Manually trigger the feedback report UI. */
  static startReport(): void {
    void YaverFeedback.launchInteractiveReport();
  }

  private static async launchInteractiveReport(): Promise<void> {
    if (!YaverFeedback.config) return;
    // Step 1 — auth. Opens the standalone LoginModal only when there is no
    // cached token. Once we have one, the overlay takes over and drives
    // step 2 (machine) + step 3 (actions) inline, so the user never sees
    // a stack of separate modals.
    const authed = await YaverFeedback.ensureAuthToken();
    if (!authed) return;

    // If the last session already landed on a reachable machine, silently
    // re-discover the agent URL so the overlay can open straight into the
    // actions view. On failure we still open the overlay — the machine
    // view just shows the issue instead of silently blocking.
    if (YaverFeedback.config.preferredDeviceId && !YaverFeedback.config.agentUrl) {
      await YaverFeedback.tryDiscoverSelectedMachine();
    } else if (YaverFeedback.config.agentUrl) {
      YaverFeedback.syncClient();
      YaverFeedback.connectCommandStream();
    }

    YaverFeedback.openReportOverlay();
  }

  private static async ensurePreferredDevice(opts: {
    forcePicker?: boolean;
  } = {}): Promise<boolean> {
    if (!YaverFeedback.config?.authToken) return false;
    if (opts.forcePicker !== true && YaverFeedback.config.preferredDeviceId) {
      return true;
    }
    try {
      const device = await openDevicePickerModal(YaverFeedback.config.authToken);
      YaverFeedback.config.preferredDeviceId = device.deviceId;
      YaverFeedback.config.agentUrl = undefined;
      return true;
    } catch {
      return false;
    }
  }

  private static async tryDiscoverSelectedMachine(): Promise<boolean> {
    if (!YaverFeedback.config?.preferredDeviceId) return false;
    const discovered = await YaverDiscovery.discover({
      convexUrl: YaverFeedback.config.convexUrl,
      authToken: YaverFeedback.config.authToken,
      preferredDeviceId: YaverFeedback.config.preferredDeviceId,
    });
    if (!discovered) return false;
    YaverFeedback.config.agentUrl = discovered.url;
    if (discovered.relayPassword) {
      YaverFeedback.config.relayPassword = discovered.relayPassword;
    }
    YaverFeedback.syncClient();
    YaverFeedback.connectCommandStream();
    return true;
  }

  private static openReportOverlay(): void {
    YaverFeedback.injectReportStyles();
    document.getElementById('yaver-feedback-overlay')?.remove();

    const overlay = document.createElement('div');
    overlay.id = 'yaver-feedback-overlay';
    overlay.innerHTML = `
      <div class="yvr-fb-shell">
        <div class="yvr-fb-card">
          <div class="yvr-fb-header">
            <div>
              <h3 class="yvr-fb-title">Yaver</h3>
              <p id="yaver-fb-subtitle" class="yvr-fb-subtitle"></p>
            </div>
            <button id="yaver-fb-close" class="yvr-fb-close" type="button" aria-label="Close">×</button>
          </div>

          <div id="yaver-fb-auth-strip" class="yvr-fb-auth-strip"></div>
          <div id="yaver-fb-body" class="yvr-fb-body"></div>

          <div id="yaver-fb-progress-track" class="yvr-fb-progress-track" style="display:none;">
            <div id="yaver-fb-progress-fill" class="yvr-fb-progress-fill"></div>
          </div>
          <p id="yaver-fb-status" class="yvr-fb-status"></p>
          <p id="yaver-fb-last-report" class="yvr-fb-last-report"></p>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);

    const subtitle = overlay.querySelector<HTMLElement>('#yaver-fb-subtitle')!;
    const authStrip = overlay.querySelector<HTMLElement>('#yaver-fb-auth-strip')!;
    const body = overlay.querySelector<HTMLElement>('#yaver-fb-body')!;
    const status = overlay.querySelector<HTMLElement>('#yaver-fb-status')!;
    const lastReport = overlay.querySelector<HTMLElement>('#yaver-fb-last-report')!;
    const progressTrack = overlay.querySelector<HTMLElement>('#yaver-fb-progress-track')!;
    const progressFill = overlay.querySelector<HTMLElement>('#yaver-fb-progress-fill')!;
    let syncVibingOnboarding: ((eligibility?: Awaited<ReturnType<typeof YaverFeedback.getVibingEligibility>>, errorMessage?: string) => void) | null = null;
    const dashboardUrl = `${(YaverFeedback.config?.authWebBaseUrl || 'https://yaver.io').replace(/\/$/, '')}/dashboard`;
    const closeBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-close')!;

    let busy = false;

    const setStatus = (message: string, progress?: number) => {
      status.textContent = message;
      if (typeof progress === 'number') {
        progressTrack.style.display = 'block';
        progressFill.style.width = `${Math.max(0, Math.min(100, progress * 100))}%`;
      } else {
        progressTrack.style.display = 'none';
        progressFill.style.width = '0%';
      }
    };

    const refreshLastReport = () => {
      const report = YaverFeedback.getLastUploadResult();
      if (!report?.id) {
        lastReport.textContent = '';
        return;
      }
      lastReport.textContent = report.changeSet?.candidateLabel
        ? `Last report: ${report.id} • ${report.changeSet.candidateLabel}`
        : `Last report: ${report.id}`;
    };

    const cleanup = () => {
      window.removeEventListener('yaver-feedback:status', statusListener);
      overlay.remove();
    };

    const close = () => {
      if (YaverFeedback.recording) {
        YaverFeedback.mediaRecorder?.stop();
        YaverFeedback.audioRecorder?.stop();
        YaverFeedback.recording = false;
      }
      cleanup();
    };

    closeBtn.onclick = close;
    overlay.onclick = (event) => {
      if (event.target === overlay) close();
    };

    const refreshAuthStrip = async () => {
      const token = YaverFeedback.config?.authToken;
      if (!token) {
        authStrip.innerHTML = '';
        return;
      }
      let user = getCachedUser();
      if (!user) {
        user = await validateToken(token);
        if (user) saveCachedUser(user);
      }
      if (!user) {
        authStrip.innerHTML =
          `<span class="yvr-fb-auth-strip-label">Session expired.</span>` +
          `<button id="yaver-fb-auth-action" class="yvr-fb-link" type="button">Sign in again</button>`;
      } else {
        const label =
          user.name && user.name !== user.email
            ? `${user.email} (${user.name})`
            : user.email;
        authStrip.innerHTML =
          `<span class="yvr-fb-auth-strip-label">Signed in as <strong>${escapeHtml(label)}</strong></span>` +
          `<button id="yaver-fb-auth-action" class="yvr-fb-link" type="button">Sign out</button>`;
      }
      const authBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-auth-action');
      if (!authBtn) return;
      authBtn.onclick = async () => {
        if (busy) return;
        if (!getCachedUser()) {
          busy = true;
          authBtn.disabled = true;
          try {
            const t = await openLoginModal();
            if (YaverFeedback.config) YaverFeedback.config.authToken = t;
            const u = await validateToken(t);
            if (u) saveCachedUser(u);
            YaverFeedback.syncClient();
            YaverFeedback.connectCommandStream();
            setStatus('Signed in. Pick a machine to continue.');
          } catch {
            setStatus('Sign-in cancelled.');
          } finally {
            busy = false;
            authBtn.disabled = false;
            await refreshAuthStrip();
            setView('machine');
          }
          return;
        }
        busy = true;
        authBtn.disabled = true;
        try {
          await YaverFeedback.signOut();
          setStatus('Signed out.');
        } finally {
          busy = false;
          authBtn.disabled = false;
          await refreshAuthStrip();
          setView('machine');
        }
      };
    };

    // ── Step 2 — Machine picker ─────────────────────────

    const renderMachineView = () => {
      subtitle.textContent = 'Step 2 of 4 — Pick the machine to connect to.';
      body.innerHTML = `
        <div class="yvr-fb-devices">
          <div class="yvr-fb-devices-head">
            <span class="yvr-fb-group-label">Your Machines</span>
            <button id="yaver-fb-refresh" class="yvr-fb-link" type="button">Refresh</button>
          </div>
          <div id="yaver-fb-owned" class="yvr-fb-devices-list"></div>

          <div class="yvr-fb-devices-head">
            <span class="yvr-fb-group-label">Shared With You</span>
          </div>
          <div id="yaver-fb-shared" class="yvr-fb-devices-list"></div>

          <p id="yaver-fb-devices-error" class="yvr-fb-devices-error"></p>
        </div>
      `;
      overlay.querySelector<HTMLButtonElement>('#yaver-fb-refresh')!.onclick = () => {
        void loadDevices();
      };
      void loadDevices();
    };

    const loadDevices = async () => {
      const ownedEl = overlay.querySelector<HTMLElement>('#yaver-fb-owned');
      const sharedEl = overlay.querySelector<HTMLElement>('#yaver-fb-shared');
      const errorEl = overlay.querySelector<HTMLElement>('#yaver-fb-devices-error');
      if (!ownedEl || !sharedEl || !errorEl) return;
      ownedEl.innerHTML = `<div class="yvr-fb-device-loading">Loading…</div>`;
      sharedEl.innerHTML = '';
      errorEl.textContent = '';
      try {
        const token = YaverFeedback.config?.authToken;
        if (!token) {
          errorEl.textContent = 'Not signed in.';
          ownedEl.innerHTML = '';
          return;
        }
        const list = await listReachableDevices(token);
        ownedEl.innerHTML =
          list.owned.length > 0
            ? list.owned.map(renderDeviceRow).join('')
            : `<p class="yvr-fb-empty">None yet. Run <code>yaver auth</code> then <code>yaver serve</code> on your machine.</p>`;
        sharedEl.innerHTML =
          list.shared.length > 0
            ? list.shared.map(renderDeviceRow).join('')
            : `<p class="yvr-fb-empty">None.</p>`;
        if (list.owned.length === 0 && list.shared.length === 0) {
          errorEl.innerHTML = `You have no access to a machine for this project. <a class="yvr-fb-inline-link" href="${escapeHtml(dashboardUrl)}" target="_blank" rel="noopener noreferrer">Open your Yaver dashboard</a>.`;
        }
        wireDeviceClicks(list.owned.concat(list.shared));
      } catch (err) {
        errorEl.textContent = err instanceof Error ? err.message : 'Failed to load machines.';
        ownedEl.innerHTML = '';
      }
    };

    const wireDeviceClicks = (all: RemoteDevice[]) => {
      overlay
        .querySelectorAll<HTMLButtonElement>('.yvr-fb-device-row[data-device-id]')
        .forEach((row) => {
          row.onclick = async () => {
            if (busy) return;
            const id = row.dataset.deviceId!;
            const device = all.find((d) => d.deviceId === id);
            if (!device) return;
            if (!device.isOnline || device.needsAuth || device.runnerDown) return;
            busy = true;
            row.classList.add('yvr-fb-device-row-busy');
            setStatus(`Connecting to ${device.name || device.deviceId}…`);
            try {
              saveSelectedDeviceId(device.deviceId);
              if (YaverFeedback.config) {
                YaverFeedback.config.preferredDeviceId = device.deviceId;
                YaverFeedback.config.agentUrl = undefined;
              }
              const discovered = await YaverFeedback.tryDiscoverSelectedMachine();
              if (!discovered) {
                setStatus('Could not reach that machine. Try again or pick another.');
                row.classList.remove('yvr-fb-device-row-busy');
                busy = false;
                return;
              }
              setStatus('');
              busy = false;
              setView('git');
            } catch (err) {
              setStatus(err instanceof Error ? err.message : 'Unable to connect.');
              row.classList.remove('yvr-fb-device-row-busy');
              busy = false;
            }
          };
        });
    };

    const renderDeviceRow = (device: RemoteDevice): string => {
      const reachable = device.isOnline && !device.needsAuth && !device.runnerDown;
      const dot = device.needsAuth
        ? 'yellow'
        : device.isOnline && !device.runnerDown
          ? 'green'
          : 'red';
      let meta = device.platform || 'Unknown platform';
      if (!device.isOnline) {
        meta = 'Offline — start `yaver serve` on this Mac';
      } else if (device.needsAuth) {
        meta = 'Needs pairing — open the Yaver app to adopt';
      } else if (device.runnerDown) {
        meta = 'Runner down — restart the coding agent';
      } else if (device.isGuest && device.hostName) {
        meta = `Shared by ${device.hostName}`;
      }
      const selected =
        YaverFeedback.config?.preferredDeviceId === device.deviceId
          ? ' yvr-fb-device-row-selected'
          : '';
      return `
        <button
          type="button"
          class="yvr-fb-device-row${selected}"
          data-device-id="${escapeHtml(device.deviceId)}"
          ${reachable ? '' : 'disabled'}
        >
          <span class="yvr-fb-dot yvr-fb-dot-${dot}"></span>
          <span class="yvr-fb-device-text">
            <span class="yvr-fb-device-name">${escapeHtml(device.name || device.deviceId)}</span>
            <span class="yvr-fb-device-meta">${escapeHtml(meta)}</span>
          </span>
          ${selected ? `<span class="yvr-fb-device-selected-badge">selected</span>` : ''}
        </button>
      `;
    };

    const connectGitForRepo = async (eligibility?: Awaited<ReturnType<typeof YaverFeedback.getVibingEligibility>>) => {
      const provider = (eligibility?.provider || '').trim().toLowerCase();
      if (provider !== 'github' && provider !== 'gitlab') {
        throw new Error('This repo does not expose a supported git host yet.');
      }
      const host = (eligibility?.repoHost || (provider === 'gitlab' ? 'gitlab.com' : 'github.com')).trim();
      const client = await YaverFeedback.getClient();
      const detected = await client.gitProviderDetect().catch(() => []);
      if (detected.some((row) => row.provider === provider && row.host === host && row.hasToken)) {
        return { kind: 'already-configured' as const };
      }
      const authToken = (YaverFeedback.config?.authToken || getCachedToken() || '').trim();
      const webBaseUrl = (YaverFeedback.config?.authWebBaseUrl || 'https://yaver.io').replace(/\/$/, '');
      const convexBaseUrl = (YaverFeedback.config?.authConvexSiteUrl || YaverFeedback.config?.convexUrl || DEFAULT_CONVEX_SITE_URL).replace(/\/$/, '');
      if (!authToken) {
        const loginUrl = `${webBaseUrl}/auth?return=${encodeURIComponent('/dashboard')}`;
        if (typeof window !== 'undefined') {
          window.open(loginUrl, '_blank', 'noopener,noreferrer');
        }
        return {
          kind: 'needs-login' as const,
          url: loginUrl,
        };
      }
      const linkResp = await fetch(`${convexBaseUrl}/auth/oauth-link/start`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${authToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          provider,
          client: 'web',
          returnTo: '/dashboard',
        }),
      });
      const linkData = await linkResp.json().catch(() => ({} as Record<string, unknown>));
      if (!linkResp.ok || typeof linkData?.token !== 'string' || !linkData.token.trim()) {
        const dashboardUrl = `${webBaseUrl}/dashboard`;
        if (typeof window !== 'undefined') {
          window.open(dashboardUrl, '_blank', 'noopener,noreferrer');
        }
        return {
          kind: 'needs-dashboard' as const,
          url: dashboardUrl,
          detail:
            typeof linkData?.error === 'string' && linkData.error.trim()
              ? linkData.error
              : 'Open the dashboard and finish provider linking there.',
        };
      }
      const oauthUrl = `${webBaseUrl}/api/auth/oauth/${provider}?client=web&intent=link&linkToken=${encodeURIComponent(
        linkData.token,
      )}&return=${encodeURIComponent('/dashboard')}`;
      if (typeof window !== 'undefined') {
        window.open(oauthUrl, '_blank', 'noopener,noreferrer');
      }
      return {
        kind: 'oauth-started' as const,
        url: oauthUrl,
        provider,
        host,
      };
    };

    const listLinkedGitProviders = async (): Promise<Set<'github' | 'gitlab'>> => {
      const authToken = (YaverFeedback.config?.authToken || getCachedToken() || '').trim();
      const convexBaseUrl = (YaverFeedback.config?.authConvexSiteUrl || YaverFeedback.config?.convexUrl || DEFAULT_CONVEX_SITE_URL).replace(/\/$/, '');
      if (!authToken) return new Set();
      const resp = await fetch(`${convexBaseUrl}/auth/providers`, {
        headers: {
          Authorization: `Bearer ${authToken}`,
        },
      });
      if (!resp.ok) return new Set();
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      const identities = Array.isArray(data?.identities) ? data.identities as Array<Record<string, unknown>> : [];
      const linked = new Set<'github' | 'gitlab'>();
      identities.forEach((identity) => {
        if (identity.provider === 'github' || identity.provider === 'gitlab') {
          linked.add(identity.provider);
        }
      });
      return linked;
    };

    const startAccountLinkProvider = async (provider: 'github' | 'gitlab') => {
      const authToken = (YaverFeedback.config?.authToken || getCachedToken() || '').trim();
      const webBaseUrl = (YaverFeedback.config?.authWebBaseUrl || 'https://yaver.io').replace(/\/$/, '');
      const convexBaseUrl = (YaverFeedback.config?.authConvexSiteUrl || YaverFeedback.config?.convexUrl || DEFAULT_CONVEX_SITE_URL).replace(/\/$/, '');
      if (!authToken) {
        const loginUrl = `${webBaseUrl}/auth?return=${encodeURIComponent('/dashboard')}`;
        if (typeof window !== 'undefined') {
          window.open(loginUrl, '_blank', 'noopener,noreferrer');
        }
        return { kind: 'needs-login' as const, url: loginUrl };
      }
      const res = await fetch(`${convexBaseUrl}/auth/oauth-link/start`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${authToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          provider,
          client: 'web',
          returnTo: '/dashboard',
        }),
      });
      const data = await res.json().catch(() => ({} as Record<string, unknown>));
      if (!res.ok || typeof data?.token !== 'string' || !data.token.trim()) {
        throw new Error(typeof data?.error === 'string' && data.error.trim() ? data.error : `Could not link ${provider}.`);
      }
      const oauthUrl = `${webBaseUrl}/api/auth/oauth/${provider}?client=web&intent=link&linkToken=${encodeURIComponent(
        data.token,
      )}&return=${encodeURIComponent('/dashboard')}`;
      if (typeof window !== 'undefined') {
        window.open(oauthUrl, '_blank', 'noopener,noreferrer');
      }
      return { kind: 'oauth-started' as const, provider, url: oauthUrl };
    };

    // ── Step 3 — Git setup ──────────────────────────────

    const renderGitSetupView = () => {
      subtitle.textContent = 'Step 3 of 4 — Set up git for this project.';
      body.innerHTML = `
        <button id="yaver-fb-machine-pill" class="yvr-fb-machine-pill" type="button">
          <span class="yvr-fb-dot yvr-fb-dot-green"></span>
          <span class="yvr-fb-machine-pill-text">
            <span id="yaver-fb-machine-pill-name" class="yvr-fb-machine-pill-name">Checking…</span>
            <span id="yaver-fb-machine-pill-meta" class="yvr-fb-machine-pill-meta"></span>
          </span>
          <span class="yvr-fb-link">Change</span>
        </button>

        <div class="yvr-fb-vibe-shell">
          <div class="yvr-fb-vibe-topline">
            <div class="yvr-fb-vibe-topline-copy">
              <label class="yvr-fb-vibe-label">Git Setup</label>
              <p id="yaver-fb-git-intro" class="yvr-fb-vibe-intro">Checking whether this machine is ready to use the repo.</p>
            </div>
            <div id="yaver-fb-git-summary" class="yvr-fb-runner-summary">Checking…</div>
          </div>

          <div class="yvr-fb-vibe-onboarding" style="display:grid;">
            <div class="yvr-fb-vibe-steps">
              <span class="yvr-fb-vibe-step" data-state="done">1. OAuth</span>
              <span class="yvr-fb-vibe-step" data-state="done">2. Machine</span>
              <span class="yvr-fb-vibe-step" data-state="active">3. Git</span>
              <span class="yvr-fb-vibe-step" data-state="upcoming">4. Vibing</span>
            </div>
            <div class="yvr-fb-vibe-stage">
              <div id="yaver-fb-git-stage-copy" class="yvr-fb-vibe-stage-copy"></div>
              <div id="yaver-fb-git-actions" class="yvr-fb-runner-auth-row">
                <div class="yvr-fb-device-loading">Checking repo access…</div>
              </div>
            </div>
          </div>
        </div>
      `;

      const machinePill = overlay.querySelector<HTMLButtonElement>('#yaver-fb-machine-pill')!;
      const gitIntro = overlay.querySelector<HTMLParagraphElement>('#yaver-fb-git-intro')!;
      const gitSummary = overlay.querySelector<HTMLDivElement>('#yaver-fb-git-summary')!;
      const gitStageCopy = overlay.querySelector<HTMLDivElement>('#yaver-fb-git-stage-copy')!;
      const gitActions = overlay.querySelector<HTMLDivElement>('#yaver-fb-git-actions')!;
      const dashboardUrl = `${(YaverFeedback.config?.authWebBaseUrl || 'https://yaver.io').replace(/\/$/, '')}/dashboard`;

      const setGitBusy = (value: boolean) => {
        busy = value;
        machinePill.disabled = value;
        gitActions.querySelectorAll<HTMLButtonElement>('button').forEach((button) => {
          button.disabled = value;
        });
      };

      const refreshGitSetup = async () => {
        setGitBusy(true);
        await refreshMachinePill();
        try {
          const eligibility = await YaverFeedback.getVibingEligibility();
          const linkedProviders = await listLinkedGitProviders().catch(() => new Set<'github' | 'gitlab'>());
          const provider = (eligibility.provider === 'github' || eligibility.provider === 'gitlab')
            ? eligibility.provider
            : null;
          const availableDevices = await YaverFeedback.listAvailableDevices().catch(() => [] as RemoteDevice[]);
          const selectedDevice = availableDevices.find((device) => device.deviceId === YaverFeedback.config?.preferredDeviceId);
          const selectedMachineOwned = !!selectedDevice && !selectedDevice.isGuest;
          const detectedProviders = await YaverFeedback.getClient()
            .then((client) => client.gitProviderDetect())
            .catch(() => []);
          const detectedProvider = provider
            ? detectedProviders.find((row) => row.provider === provider)
            : null;
          const gitReady = eligibility.needsGitSetup !== true;
          // The agent now returns repoBindingSource ∈ {"git","registry"} so
          // the SDK can tell the user whether it learned the remote from a
          // local clone or from a registry entry the host declared earlier.
          const repoBindingSource = (eligibility as { repoBindingSource?: string }).repoBindingSource;
          const repoFullName = (eligibility as { repoFullName?: string }).repoFullName;
          const repoHost = (eligibility as { repoHost?: string }).repoHost;
          const repoBound = !!provider && !!repoFullName;
          const needsRemoteDeclaration = !provider && selectedMachineOwned;
          gitSummary.textContent = gitReady
            ? 'Git configured'
            : repoBound
              ? `Bound to ${repoFullName}`
              : needsRemoteDeclaration
                ? 'Project remote unknown'
                : 'Git setup needed';
          gitIntro.textContent = gitReady
            ? 'This project is already connected on the selected machine.'
            : needsRemoteDeclaration
              ? 'Yaver does not know which repository this project belongs to. Tell it the GitHub or GitLab URL so it can verify access and clone on demand.'
              : 'Link your git provider to Yaver for account identity, then configure the selected machine from web/mobile/SSH before vibing unlocks.';
          const stageDetail = repoBound
            ? `<div class="yvr-fb-vibe-stage-meta">${escapeHtml(
                `${repoFullName} on ${repoHost || (provider === 'gitlab' ? 'gitlab.com' : 'github.com')}` +
                (repoBindingSource === 'registry' ? ' (declared via web SDK)' : ''),
              )}</div>`
            : '';
          gitStageCopy.innerHTML = `
            <div class="yvr-fb-vibe-stage-title">${gitReady ? 'Git Ready' : repoBound ? 'Authorize machine' : needsRemoteDeclaration ? 'Bind project to a repo' : 'Connect Git'}</div>
            <div class="yvr-fb-vibe-stage-text">${escapeHtml(
              gitReady
                ? 'Git is configured for this project on the selected machine. Continue when you want to open the vibing page.'
                : needsRemoteDeclaration
                  ? 'Pick GitHub or GitLab below and paste the repo URL plus a Personal Access Token. Yaver will save the binding on the selected machine and verify the repo is visible to your account.'
                  : eligibility.guidance?.trim()
                    ? `${eligibility.reason ?? 'Git is not configured for this project.'} ${eligibility.guidance}`
                    : eligibility.reason ?? 'Git is not configured for this project.',
            )}</div>
            ${stageDetail}
          `;
          if (gitReady) {
            gitActions.innerHTML = `
              <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="next">
                <span class="yvr-fb-runner-card-kicker">Repo access</span>
                <span class="yvr-fb-runner-card-title">Git configured</span>
                <span class="yvr-fb-runner-card-meta">This machine can use the repo for this project.</span>
                <span class="yvr-fb-runner-card-action">Next</span>
              </button>
            `;
            gitActions.querySelector<HTMLButtonElement>('[data-git-action="next"]')!.onclick = () => {
              if (!busy) {
                setStatus('');
                setView('actions');
              }
            };
          } else if (needsRemoteDeclaration) {
            const projectName = (eligibility as { projectName?: string }).projectName || 'this project';
            gitActions.innerHTML = `
              <div class="yvr-fb-runner-card yvr-fb-git-card">
                <span class="yvr-fb-runner-card-kicker">Bind project</span>
                <span class="yvr-fb-runner-card-title">Where does ${escapeHtml(projectName)} live?</span>
                <span class="yvr-fb-runner-card-meta">Yaver stores this binding on the selected machine so eligibility, vibing, and clone-on-demand all know the canonical repo.</span>
                <div class="yvr-fb-vibe-radio-row" role="radiogroup" aria-label="Provider">
                  <label class="yvr-fb-vibe-radio">
                    <input type="radio" name="yaver-fb-bootstrap-provider" value="github" checked />
                    <span>GitHub</span>
                  </label>
                  <label class="yvr-fb-vibe-radio">
                    <input type="radio" name="yaver-fb-bootstrap-provider" value="gitlab" />
                    <span>GitLab</span>
                  </label>
                </div>
                <input id="yaver-fb-bootstrap-url" class="yvr-fb-vibe-input" type="text" placeholder="https://github.com/owner/repo or git@github.com:owner/repo.git" autocomplete="off" />
                <input id="yaver-fb-bootstrap-host" class="yvr-fb-vibe-input" type="text" placeholder="gitlab.com (override for self-hosted GitLab)" autocomplete="off" style="display:none;" />
                <input id="yaver-fb-bootstrap-token" class="yvr-fb-vibe-input" type="password" placeholder="ghp_... (Personal Access Token)" autocomplete="off" />
                <button type="button" class="yvr-fb-action yvr-fb-action-vibe" data-git-action="bootstrap-save">Save &amp; verify</button>
              </div>
              <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="detect">
                <span class="yvr-fb-runner-card-kicker">Selected machine</span>
                <span class="yvr-fb-runner-card-title">Discover existing git auth</span>
                <span class="yvr-fb-runner-card-meta">Skip the form if the machine already has gh/glab signed in or a credential helper configured.</span>
                <span class="yvr-fb-runner-card-action">Detect</span>
              </button>
              <a class="yvr-fb-runner-card yvr-fb-git-card" data-git-link="dashboard" href="${escapeHtml(dashboardUrl)}" target="_blank" rel="noopener noreferrer">
                <span class="yvr-fb-runner-card-kicker">Yaver web</span>
                <span class="yvr-fb-runner-card-title">Open dashboard</span>
                <span class="yvr-fb-runner-card-meta">Settings → link GitHub/GitLab to your Yaver account, or use Tools to configure machines from there.</span>
                <span class="yvr-fb-runner-card-action">Open</span>
              </a>
            `;

            const radios = gitActions.querySelectorAll<HTMLInputElement>('input[name="yaver-fb-bootstrap-provider"]');
            const urlInput = gitActions.querySelector<HTMLInputElement>('#yaver-fb-bootstrap-url')!;
            const hostInput = gitActions.querySelector<HTMLInputElement>('#yaver-fb-bootstrap-host')!;
            const tokenInput = gitActions.querySelector<HTMLInputElement>('#yaver-fb-bootstrap-token')!;
            const saveBtn = gitActions.querySelector<HTMLButtonElement>('[data-git-action="bootstrap-save"]')!;

            const syncProviderUI = () => {
              const selected = (Array.from(radios).find((r) => r.checked)?.value || 'github') as 'github' | 'gitlab';
              hostInput.style.display = selected === 'gitlab' ? '' : 'none';
              tokenInput.placeholder = selected === 'gitlab' ? 'glpat-... (Personal Access Token)' : 'ghp_... (Personal Access Token)';
            };
            radios.forEach((radio) => {
              radio.onchange = syncProviderUI;
            });
            syncProviderUI();

            saveBtn.onclick = async () => {
              if (busy) return;
              const selected = (Array.from(radios).find((r) => r.checked)?.value || 'github') as 'github' | 'gitlab';
              const remoteUrl = urlInput.value.trim();
              const customHost = hostInput.value.trim();
              const token = tokenInput.value.trim();
              if (!remoteUrl) {
                setStatus('Paste the repo URL first.');
                return;
              }
              if (!token) {
                setStatus('Paste a Personal Access Token first.');
                return;
              }
              setGitBusy(true);
              try {
                const client = await YaverFeedback.getClient();
                setStatus(`Saving ${selected === 'gitlab' ? 'GitLab' : 'GitHub'} token on the selected machine…`);
                await client.gitProviderSetup({
                  provider: selected,
                  token,
                  host: selected === 'gitlab' ? (customHost || 'gitlab.com') : undefined,
                });
                setStatus('Recording project remote on the selected machine…');
                const projectName =
                  (eligibility as { projectName?: string }).projectName ||
                  YaverFeedback.projectIdentity()?.projectName ||
                  '';
                if (!projectName) {
                  throw new Error('Cannot bind the project — its name is unknown to the SDK.');
                }
                await client.setProjectRemote({ projectName, remoteUrl });
                tokenInput.value = '';
                setStatus('Project remote saved. Verifying repo access…');
              } catch (err) {
                setStatus(err instanceof Error ? err.message : 'Could not save the project remote.');
              } finally {
                await refreshGitSetup();
              }
            };

            gitActions.querySelector<HTMLButtonElement>('[data-git-action="detect"]')!.onclick = async () => {
              if (busy) return;
              setGitBusy(true);
              try {
                setStatus('Detecting existing git credentials on the selected machine…');
                const detected = await YaverFeedback.getClient().then((client) => client.gitProviderDetect());
                setStatus(
                  detected.length > 0
                    ? `Detected ${detected.map((row) => row.provider).join(', ')}. Re-checking eligibility…`
                    : 'No machine-side git credentials were detected. Use the form above.',
                );
              } catch (err) {
                setStatus(err instanceof Error ? err.message : 'Could not detect git credentials.');
              } finally {
                await refreshGitSetup();
              }
            };
          } else {
            const providerTitle = provider === 'gitlab' ? 'GitLab' : 'GitHub';
            const linkState = provider && linkedProviders.has(provider) ? `${providerTitle} linked to Yaver` : `Link ${providerTitle} to Yaver`;
            gitActions.innerHTML = `
              ${provider ? `
              <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="link-account">
                <span class="yvr-fb-runner-card-kicker">Main flow</span>
                <span class="yvr-fb-runner-card-title">${escapeHtml(linkState)}</span>
                <span class="yvr-fb-runner-card-meta">This links the provider to your Yaver account for sign-in and recovery. Machine repo access still needs a machine token or existing local git auth.</span>
                <span class="yvr-fb-runner-card-action">${linkedProviders.has(provider) ? 'Open' : 'Link'}</span>
              </button>` : ''}
              <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="detect">
                <span class="yvr-fb-runner-card-kicker">Selected machine</span>
                <span class="yvr-fb-runner-card-title">Discover existing git auth</span>
                <span class="yvr-fb-runner-card-meta">${escapeHtml(
                  detectedProvider?.username
                    ? `Detected ${detectedProvider.username} on ${detectedProvider.host || providerTitle.toLowerCase()}.`
                    : 'Try remote gh/glab or existing machine credentials first.',
                )}</span>
                <span class="yvr-fb-runner-card-action">Detect</span>
              </button>
              ${provider && selectedMachineOwned ? `
              <div class="yvr-fb-runner-card yvr-fb-git-card">
                <span class="yvr-fb-runner-card-kicker">Owned machine</span>
                <span class="yvr-fb-runner-card-title">Set machine ${escapeHtml(providerTitle)} token</span>
                <span class="yvr-fb-runner-card-meta">Paste a ${escapeHtml(providerTitle)} token here to configure git directly on this selected machine.</span>
                <input id="yaver-fb-git-token" class="yvr-fb-vibe-input" type="password" placeholder="${escapeHtml(provider === 'gitlab' ? 'glpat-...' : 'ghp_...')}" />
                ${provider === 'gitlab' ? '<input id="yaver-fb-git-host" class="yvr-fb-vibe-input" type="text" placeholder="gitlab.com" />' : ''}
                <button type="button" class="yvr-fb-action yvr-fb-action-secondary" data-git-action="save-machine-token">Save on machine</button>
              </div>` : ''}
              <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="connect">
                <span class="yvr-fb-runner-card-kicker">Repo access</span>
                <span class="yvr-fb-runner-card-title">Manual machine setup</span>
                <span class="yvr-fb-runner-card-meta">${escapeHtml(
                  selectedMachineOwned
                    ? 'If direct setup here does not work, finish machine onboarding from Yaver web UI, Yaver mobile settings, or your own SSH session.'
                    : 'This machine is shared with you. You can link your git account in Yaver here, but machine-side git setup must be done by the host or on one of your own machines.',
                )}</span>
                <span class="yvr-fb-runner-card-action">Guide</span>
              </button>
              <a class="yvr-fb-runner-card yvr-fb-git-card" data-git-link="dashboard" href="${escapeHtml(dashboardUrl)}" target="_blank" rel="noopener noreferrer">
                <span class="yvr-fb-runner-card-kicker">Yaver web</span>
                <span class="yvr-fb-runner-card-title">Open dashboard</span>
                <span class="yvr-fb-runner-card-meta">Use Settings to link GitHub/GitLab to your Yaver account, then separately configure machine repo access from Git or Tools for one or more machines.</span>
                <span class="yvr-fb-runner-card-action">Open</span>
              </a>
              <div class="yvr-fb-runner-card yvr-fb-git-card" aria-hidden="true">
                <span class="yvr-fb-runner-card-kicker">Yaver mobile / SSH</span>
                <span class="yvr-fb-runner-card-title">Other recovery paths</span>
                <span class="yvr-fb-runner-card-meta">Mobile app: Settings → Remote machine onboarding. SSH path: use your usual git/gh/glab setup on the machine, then come back and press Detect.</span>
                <span class="yvr-fb-runner-card-action">Info</span>
              </div>
            `;
            if (provider) {
              gitActions.querySelector<HTMLButtonElement>('[data-git-action="link-account"]')!.onclick = async () => {
                if (busy) return;
                setGitBusy(true);
                try {
                  setStatus(`Linking ${providerTitle} to your Yaver account…`);
                  const result = await startAccountLinkProvider(provider);
                  if (result.kind === 'needs-login') {
                    setStatus('Sign in to Yaver in the opened tab, then come back here.');
                    return;
                  }
                  setStatus(`Finish ${providerTitle} sign-in in the opened tab. After that, if this machine still cannot see the repo, use Detect or machine onboarding.`);
                } catch (err) {
                  setStatus(err instanceof Error ? err.message : `Could not link ${providerTitle}.`);
                } finally {
                  setGitBusy(false);
                }
              };
            }
            if (provider && selectedMachineOwned) {
              gitActions.querySelector<HTMLButtonElement>('[data-git-action="save-machine-token"]')!.onclick = async () => {
                if (busy) return;
                const tokenInput = gitActions.querySelector<HTMLInputElement>('#yaver-fb-git-token');
                const hostInput = gitActions.querySelector<HTMLInputElement>('#yaver-fb-git-host');
                const token = tokenInput?.value.trim() || '';
                const host = hostInput?.value.trim() || '';
                if (!token) {
                  setStatus(`Paste a ${providerTitle} token first.`);
                  return;
                }
                setGitBusy(true);
                try {
                  setStatus(`Saving ${providerTitle} token on the selected machine…`);
                  await YaverFeedback.getClient().then((client) => client.gitProviderSetup({
                    provider,
                    token,
                    host: provider === 'gitlab' ? (host || 'gitlab.com') : undefined,
                  }));
                  if (tokenInput) tokenInput.value = '';
                  if (hostInput) hostInput.value = provider === 'gitlab' ? (host || 'gitlab.com') : '';
                  setStatus(`${providerTitle} is configured on the selected machine. Re-checking repo access…`);
                } catch (err) {
                  setStatus(err instanceof Error ? err.message : `Could not save ${providerTitle} token.`);
                } finally {
                  await refreshGitSetup();
                }
              };
            }
            gitActions.querySelector<HTMLButtonElement>('[data-git-action="detect"]')!.onclick = async () => {
              if (busy) return;
              setGitBusy(true);
              try {
                setStatus('Detecting existing git credentials on the selected machine…');
                const detected = await YaverFeedback.getClient().then((client) => client.gitProviderDetect());
                setStatus(
                  detected.length > 0
                    ? `Detected ${detected.map((row) => row.provider).join(', ')} on the selected machine. Re-checking setup…`
                    : 'No machine-side git credentials were detected. Use dashboard, mobile settings, or SSH setup.',
                );
              } catch (err) {
                setStatus(err instanceof Error ? err.message : 'Could not detect git credentials.');
              } finally {
                await refreshGitSetup();
              }
            };
            gitActions.querySelector<HTMLButtonElement>('[data-git-action="connect"]')!.onclick = async () => {
              if (busy) return;
              try {
                setStatus('Use one of these paths: 1) Yaver web dashboard, 2) Yaver mobile Settings → Remote machine onboarding, or 3) your own SSH session on the machine. Then press Detect.');
              } finally {
                setGitBusy(false);
              }
            };
          }
        } catch (err) {
          gitSummary.textContent = 'Machine unavailable';
          gitIntro.textContent = 'Pick a reachable machine before continuing.';
          gitStageCopy.innerHTML = `
            <div class="yvr-fb-vibe-stage-title">Machine Required</div>
            <div class="yvr-fb-vibe-stage-text">${escapeHtml(err instanceof Error ? err.message : 'Could not check git setup.')}</div>
          `;
          gitActions.innerHTML = `
            <button type="button" class="yvr-fb-runner-card" data-git-action="machine">
              <span class="yvr-fb-runner-card-kicker">Connection</span>
              <span class="yvr-fb-runner-card-title">Pick machine</span>
              <span class="yvr-fb-runner-card-meta">Go back and select a reachable machine.</span>
              <span class="yvr-fb-runner-card-action">Back</span>
            </button>
          `;
          gitActions.querySelector<HTMLButtonElement>('[data-git-action="machine"]')!.onclick = () => {
            if (!busy) setView('machine');
          };
          setStatus(err instanceof Error ? err.message : 'Could not check git setup.');
        } finally {
          setGitBusy(false);
        }
      };

      machinePill.onclick = () => {
        if (!busy) setView('machine');
      };

      void refreshGitSetup();
    };

    // ── Step 4 — Actions / vibing ───────────────────────

    const renderActionsView = () => {
      subtitle.textContent = 'Step 4 of 4 — Vibing tools and chat.';
      body.innerHTML = `
        <button id="yaver-fb-machine-pill" class="yvr-fb-machine-pill" type="button">
          <span class="yvr-fb-dot yvr-fb-dot-green"></span>
          <span class="yvr-fb-machine-pill-text">
            <span id="yaver-fb-machine-pill-name" class="yvr-fb-machine-pill-name">Checking…</span>
            <span id="yaver-fb-machine-pill-meta" class="yvr-fb-machine-pill-meta"></span>
          </span>
          <span class="yvr-fb-link">Change</span>
        </button>

        <div class="yvr-fb-toolbar">
          <button id="yaver-fb-screenshot" class="yvr-fb-tool yvr-fb-tool-screenshot" type="button" title="Screenshot + note">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/></svg>
            <span>Screenshot</span>
          </button>
          <button id="yaver-fb-reload" class="yvr-fb-tool yvr-fb-tool-reload" type="button" title="Hot reload">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 4v5h5"/><path d="M20 20v-5h-5"/><path d="M5.5 9A7.5 7.5 0 0 1 19 8.5"/><path d="M18.5 15A7.5 7.5 0 0 1 5 15.5"/></svg>
            <span>Reload</span>
          </button>
          <button id="yaver-fb-commit" class="yvr-fb-tool yvr-fb-tool-commit" type="button" title="Commit, rebase, and push">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3v18"/><path d="M7 8l5-5 5 5"/><path d="M7 16l5 5 5-5"/></svg>
            <span>Commit</span>
          </button>
          <button id="yaver-fb-deploy" class="yvr-fb-tool yvr-fb-tool-deploy" type="button" title="Deploy to production">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3l3.5 7H22l-5 4.5 2 6.5-7-4-7 4 2-6.5L2 10h6.5z"/></svg>
            <span>Deploy</span>
          </button>
        </div>

        <div class="yvr-fb-vibe-shell">
          <div class="yvr-fb-vibe-topline">
            <div class="yvr-fb-vibe-topline-copy">
              <label class="yvr-fb-vibe-label" for="yaver-fb-vibe-prompt">Vibing</label>
              <p id="yaver-fb-vibe-intro" class="yvr-fb-vibe-intro">Use tools, chat, and vibing from the selected machine.</p>
            </div>
            <div id="yaver-fb-runner-summary" class="yvr-fb-runner-summary">Checking agent…</div>
          </div>
            <div id="yaver-fb-vibe-onboarding" class="yvr-fb-vibe-onboarding">
            <div id="yaver-fb-vibe-steps" class="yvr-fb-vibe-steps"></div>
            <div class="yvr-fb-vibe-stage">
              <div id="yaver-fb-vibe-stage-copy" class="yvr-fb-vibe-stage-copy"></div>
              <div id="yaver-fb-runner-actions" class="yvr-fb-runner-auth-row">
                <div class="yvr-fb-device-loading">Checking remote agents…</div>
              </div>
            </div>
          </div>

          <div class="yvr-fb-vibe-block">
            <div id="yaver-fb-vibe-gate" class="yvr-fb-vibe-gate" style="display:none;"></div>
            <button id="yaver-fb-vibe-repair" class="yvr-fb-action yvr-fb-action-secondary" type="button" style="display:none;">Continue Setup</button>
            <textarea id="yaver-fb-vibe-prompt" class="yvr-fb-vibe-input" placeholder="Describe what Yaver should work on next..."></textarea>
            <button id="yaver-fb-vibe" class="yvr-fb-action yvr-fb-action-vibe" type="button">Start Vibing Task</button>
          </div>
        </div>
      `;

      const screenshotBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-screenshot')!;
      const reloadBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-reload')!;
      const commitBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-commit')!;
      const deployBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-deploy')!;
      const vibeBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-vibe')!;
      const vibeRepairBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-vibe-repair')!;
      const vibePrompt = overlay.querySelector<HTMLTextAreaElement>('#yaver-fb-vibe-prompt')!;
      const machinePill = overlay.querySelector<HTMLButtonElement>('#yaver-fb-machine-pill')!;
      const runnerActions = overlay.querySelector<HTMLDivElement>('#yaver-fb-runner-actions')!;
      const runnerSummary = overlay.querySelector<HTMLDivElement>('#yaver-fb-runner-summary')!;
      const vibeIntro = overlay.querySelector<HTMLParagraphElement>('#yaver-fb-vibe-intro')!;
      const vibeOnboarding = overlay.querySelector<HTMLDivElement>('#yaver-fb-vibe-onboarding')!;
      const vibeSteps = overlay.querySelector<HTMLDivElement>('#yaver-fb-vibe-steps')!;
      const vibeStageCopy = overlay.querySelector<HTMLDivElement>('#yaver-fb-vibe-stage-copy')!;
      let cachedRunners: Awaited<ReturnType<typeof YaverFeedback.getAvailableRunners>> = [];

      const renderRunnerCards = (runners: typeof cachedRunners) => {
        if (!runners.length) {
          return `<div class="yvr-fb-empty">No remote coding agents were detected.</div>`;
        }
        return runners
          .map((runner) => {
            const actionLabel = runner.isDefault
              ? 'Primary'
              : runner.ready
                ? 'Confirm'
                : supportsRunnerBrowserAuth(runner.id)
                  ? 'Sign In'
                  : 'Configure';
            const detail = runner.ready
              ? runner.authSource
                ? `Authenticated via ${runner.authSource}`
                : 'Authenticated'
              : runner.error || runner.warning || (runner.installed ? 'Needs authentication' : 'Not installed');
            return `
              <button type="button" class="yvr-fb-runner-card" data-runner="${escapeHtml(runner.id)}">
                <span class="yvr-fb-runner-card-kicker">${runner.isDefault ? 'Primary agent' : 'Available agent'}</span>
                <span class="yvr-fb-runner-card-title">${escapeHtml(runner.name || runner.id)}</span>
                <span class="yvr-fb-runner-card-meta">${escapeHtml(detail)}</span>
                <span class="yvr-fb-runner-card-action">${escapeHtml(actionLabel)}</span>
              </button>
            `;
          })
          .join('');
      };

      const renderRunnerSummary = (runners: typeof cachedRunners) => {
        const primary = runners.find((runner) => runner.isDefault) || runners.find((runner) => runner.ready) || runners[0];
        if (!primary) {
          runnerSummary.textContent = 'No agent';
          return;
        }
        runnerSummary.textContent = primary.ready
          ? `${primary.name || primary.id} ready`
          : `${primary.name || primary.id} needs setup`;
      };

      const renderOnboarding = (eligibility?: Awaited<ReturnType<typeof YaverFeedback.getVibingEligibility>>, errorMessage?: string) => {
        const hasReadyRunner = cachedRunners.some((runner) => runner.ready);
        const needsAgent = !eligibility?.canVibe && (!!eligibility?.needsRunnerAuth || !hasReadyRunner);
        const needsGit = !eligibility?.canVibe && !needsAgent && eligibility?.needsGitSetup === true;
        const steps = [
          {
            id: 'agent',
            label: '1. OAuth',
            state: eligibility?.canVibe ? 'done' : needsAgent ? 'active' : 'done',
          },
          {
            id: 'machine',
            label: '2. Machine',
            state: 'done',
          },
          {
            id: 'git',
            label: '3. Git',
            state: eligibility?.canVibe ? 'done' : needsGit ? 'active' : 'done',
          },
          {
            id: 'chat',
            label: '4. Vibing',
            state: eligibility?.canVibe ? 'active' : 'upcoming',
          },
        ];
        vibeSteps.innerHTML = steps
          .map((step) => `<span class="yvr-fb-vibe-step" data-state="${step.state}">${escapeHtml(step.label)}</span>`)
          .join('');

        if (eligibility?.canVibe) {
          vibeOnboarding.style.display = 'none';
          vibeIntro.textContent = 'The selected machine is ready.';
          return;
        }

        if (!needsAgent && !needsGit) {
          vibeOnboarding.style.display = 'none';
          vibeIntro.textContent = 'Yaver is checking the remaining access rules.';
          return;
        }

        vibeOnboarding.style.display = 'grid';
        if (needsAgent) {
          vibeIntro.textContent = 'Finish coding-agent sign-in for this machine.';
          vibeStageCopy.innerHTML = `
            <div class="yvr-fb-vibe-stage-title">Select Agent</div>
            <div class="yvr-fb-vibe-stage-text">Pick the runner you want on this machine and finish its sign-in flow.</div>
          `;
          runnerActions.style.display = 'grid';
          if (!cachedRunners.length && errorMessage) {
            runnerActions.innerHTML = `<div class="yvr-fb-empty">${escapeHtml(errorMessage)}</div>`;
          } else if (!cachedRunners.length) {
            runnerActions.innerHTML = `<div class="yvr-fb-device-loading">Checking remote agents…</div>`;
          }
          return;
        }

        vibeIntro.textContent = 'Git still needs to be connected before vibing can start.';
        vibeStageCopy.innerHTML = `
          <div class="yvr-fb-vibe-stage-title">Connect Git</div>
          <div class="yvr-fb-vibe-stage-text">${escapeHtml(
            eligibility?.guidance?.trim()
              ? `${eligibility.reason ?? 'This repo is not ready yet.'} ${eligibility.guidance}`
              : eligibility?.reason ?? 'Finish repo connection after agent sign-in.',
          )}</div>
        `;
        runnerActions.style.display = 'grid';
        runnerActions.innerHTML = `
          <button type="button" class="yvr-fb-runner-card yvr-fb-git-card" data-git-action="connect">
            <span class="yvr-fb-runner-card-kicker">Repo access</span>
            <span class="yvr-fb-runner-card-title">Connect Git</span>
            <span class="yvr-fb-runner-card-meta">Use the machine's git settings for ${escapeHtml(eligibility?.repoHost || eligibility?.provider || 'this repo')}.</span>
            <span class="yvr-fb-runner-card-action">Connect</span>
          </button>
        `;
        runnerActions.querySelectorAll<HTMLButtonElement>('[data-git-action="connect"]').forEach((button) => {
          button.disabled = busy;
          button.onclick = async () => {
            if (busy) return;
            setActionsBusy(true);
            try {
              setStatus('Connecting git for this repo…');
              const result = await connectGitForRepo(eligibility);
              if (result.kind === 'oauth-started') {
                setStatus(`Finish ${result.provider === 'gitlab' ? 'GitLab' : 'GitHub'} sign-in in the opened Yaver tab, then go back to git setup.`);
                return;
              }
              if (result.kind === 'needs-login') {
                setStatus('Sign in to Yaver in the opened tab, then go back to git setup.');
                return;
              }
              if (result.kind === 'needs-dashboard') {
                setStatus(result.detail || 'Open the Yaver dashboard in the new tab and finish provider linking there.');
                return;
              }
              await Promise.all([refreshMachinePill(), refreshVibingGate()]);
              const updated = await YaverFeedback.getVibingEligibility();
              setStatus(updated.canVibe ? 'Git is ready for this repo.' : updated.guidance || updated.reason || 'Git setup still needs attention.');
            } catch (err) {
              setStatus(err instanceof Error ? err.message : 'Could not connect git.');
            } finally {
              setActionsBusy(false);
            }
          };
        });
      };
      syncVibingOnboarding = renderOnboarding;

      const afterRunnerAuth = async () => {
        await Promise.all([refreshMachinePill(), refreshVibingGate()]);
      };

      const refreshRunnerActions = async () => {
        try {
          const runners = await YaverFeedback.getAvailableRunners();
          cachedRunners = runners;
          renderRunnerSummary(runners);
          runnerActions.innerHTML = renderRunnerCards(runners);
          runnerActions.querySelectorAll<HTMLButtonElement>('[data-runner]').forEach((button) => {
            button.disabled = busy;
            button.onclick = async () => {
              if (busy) return;
              const runner = button.dataset.runner || '';
              setActionsBusy(true);
              try {
                const selected = cachedRunners.find((item) => item.id === runner);
                if (selected?.ready) {
                  setStatus(`Setting ${runner} as the primary agent…`);
                  const result = await YaverFeedback.switchRunner(runner);
                  setStatus(`${result.runner} is now the primary agent.`);
                } else if (supportsRunnerBrowserAuth(runner)) {
                  setStatus(`Starting ${runner} sign-in on the remote machine…`);
                  await YaverFeedback.signInRunner(runner);
                } else {
                  setStatus(`Configuring ${runner} on the remote machine…`);
                  const result = await YaverFeedback.setupRunnerAuth(runner);
                  if (!result) {
                    setStatus(`Cancelled ${runner} setup.`);
                    return;
                  }
                  setStatus(result.detail || `${runner} is configured.`);
                }
                await Promise.all([refreshRunnerActions(), afterRunnerAuth()]);
              } catch (err) {
                setStatus(err instanceof Error ? err.message : `Could not configure ${runner}.`);
              } finally {
                setActionsBusy(false);
              }
            };
          });
          renderOnboarding();
        } catch (err) {
          const message = err instanceof Error ? err.message : 'Could not load agent selection.';
          cachedRunners = [];
          renderRunnerSummary([]);
          renderOnboarding(undefined, message);
          runnerActions.innerHTML = `<div class="yvr-fb-empty">${escapeHtml(message)}</div>`;
        }
      };

      const setActionsBusy = (value: boolean) => {
        busy = value;
        [screenshotBtn, reloadBtn, commitBtn, deployBtn, vibeBtn, vibeRepairBtn, machinePill].forEach(
          (el) => ((el as HTMLButtonElement).disabled = value),
        );
        runnerActions.querySelectorAll<HTMLButtonElement>('[data-runner], [data-git-action]').forEach((el) => {
          el.disabled = value;
        });
        vibePrompt.disabled = value;
      };

      vibeRepairBtn.onclick = async () => {
        setActionsBusy(true);
        try {
          setStatus('Checking machine sign-in…');
          const client = await YaverFeedback.getClient();
          await client.recoverAgentAuth().catch(() => false);
          await Promise.all([refreshMachinePill(), refreshRunnerActions(), refreshVibingGate()]);
          let eligibility = await YaverFeedback.getVibingEligibility();
          if (!eligibility.canVibe && eligibility.needsRunnerAuth && eligibility.runner) {
            if (supportsRunnerBrowserAuth(eligibility.runner)) {
              setStatus(`Signing into ${eligibility.runner} on the remote machine…`);
              await YaverFeedback.signInRunner(eligibility.runner);
            } else {
              setStatus(`Configuring ${eligibility.runner} on the remote machine…`);
              const result = await YaverFeedback.setupRunnerAuth(eligibility.runner);
              if (!result) {
                setStatus(`Cancelled ${eligibility.runner} setup.`);
                return;
              }
            }
            await Promise.all([refreshMachinePill(), refreshRunnerActions(), refreshVibingGate()]);
            eligibility = await YaverFeedback.getVibingEligibility();
          }
          if (!eligibility.canVibe && eligibility.needsGitSetup) {
            setStatus('Connecting git for this repo…');
            const result = await connectGitForRepo(eligibility);
            if (result.kind === 'oauth-started') {
              setStatus(`Finish ${result.provider === 'gitlab' ? 'GitLab' : 'GitHub'} sign-in in the opened Yaver tab, then click Continue Setup again.`);
              return;
            }
            if (result.kind === 'needs-login') {
              setStatus('Sign in to Yaver in the opened tab, then click Continue Setup again.');
              return;
            }
            if (result.kind === 'needs-dashboard') {
              setStatus(result.detail || 'Open the Yaver dashboard in the new tab and finish provider linking there.');
              return;
            }
            await Promise.all([refreshMachinePill(), refreshRunnerActions(), refreshVibingGate()]);
            eligibility = await YaverFeedback.getVibingEligibility();
          }
          if (eligibility.canVibe) {
            setStatus('Vibing is ready.');
          } else {
            setStatus(
              eligibility.guidance && eligibility.guidance.trim()
                ? `${eligibility.reason ?? 'Vibing unavailable.'} ${eligibility.guidance}`
                : eligibility.reason ?? 'Vibing unavailable.',
            );
          }
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Could not prepare vibing.');
        } finally {
          setActionsBusy(false);
        }
      };

      machinePill.onclick = () => {
        if (!busy) setView('machine');
      };

      screenshotBtn.onclick = async () => {
        const note = prompt('Describe this bug (optional):') || '';
        setActionsBusy(true);
        setStatus('Capturing screenshot…');
        try {
          const captured = await YaverFeedback.captureScreenshotBlob({
            overlay,
            annotation: note,
          });
          if (!captured) {
            setStatus('Screenshot capture failed in this browser.');
            return;
          }
          setStatus(`Screenshot captured${note ? `: ${note}` : ''}`);
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Screenshot capture failed.');
        } finally {
          setActionsBusy(false);
        }
      };

      reloadBtn.onclick = async () => {
        setActionsBusy(true);
        setStatus('Requesting reload…');
        try {
          const ack = await YaverFeedback.reloadApp('dev');
          await YaverFeedback.switchToRemoteDevServerIfNeeded();
          setStatus(ack.message);
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Reload failed.');
        } finally {
          setActionsBusy(false);
        }
      };

      commitBtn.onclick = async () => {
        const message = prompt('Commit message (leave blank for automatic message):') || '';
        setActionsBusy(true);
        setStatus('Committing, rebasing, and pushing…');
        try {
          const result = await YaverFeedback.commitProject(message.trim() || undefined);
          setStatus(
            result.commit
              ? `Committed ${result.commit}${result.branch ? ` on ${result.branch}` : ''}.`
              : result.message || 'Commit completed.',
          );
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Commit failed.');
        } finally {
          setActionsBusy(false);
        }
      };

      deployBtn.onclick = async () => {
        setActionsBusy(true);
        setStatus('Starting production deploy…');
        try {
          const result = await YaverFeedback.deployProject();
          setStatus(result.message || (result.taskId ? `Deploy started: ${result.taskId}` : 'Deploy started.'));
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Deploy failed.');
        } finally {
          setActionsBusy(false);
        }
      };

      vibeBtn.onclick = async () => {
        const promptText = vibePrompt.value.trim();
        if (!promptText) {
          setStatus('Enter a vibing prompt first.');
          return;
        }
        setActionsBusy(true);
        setStatus('Checking vibing access…');
        try {
          const eligibility = await YaverFeedback.getVibingEligibility();
          if (!eligibility.canVibe) {
            setStatus(
              eligibility.guidance && eligibility.guidance.trim()
                ? `${eligibility.reason ?? 'Vibing unavailable.'} ${eligibility.guidance}`
                : eligibility.reason ?? 'Vibing unavailable.',
            );
            return;
          }
          setStatus('Creating vibing task…');
          const result = await YaverFeedback.vibing(promptText);
          setStatus(`Vibing task created: ${result.taskId}`);
          vibePrompt.value = '';
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Vibing failed.');
        } finally {
          setActionsBusy(false);
        }
      };

      void refreshMachinePill();
      void refreshRunnerActions();
      void refreshVibingGate();
    };

    /**
     * Gate Vibing based on agent eligibility. /vibing/eligibility on the
     * selected agent tells us whether the machine has an authenticated
     * coding runner AND whether the caller has enough scope/access to
     * kick a task. If not, we disable the prompt + button and show the
     * reason so the user doesn't click a doomed CTA.
     *
     * We also re-check after every successful remote sign-in, so the gate
     * unblocks automatically once codex/claude auth lands.
     */
    const refreshVibingGate = async () => {
      const gate = overlay.querySelector<HTMLDivElement>('#yaver-fb-vibe-gate');
      const vibePromptEl = overlay.querySelector<HTMLTextAreaElement>('#yaver-fb-vibe-prompt');
      const vibeBtnEl = overlay.querySelector<HTMLButtonElement>('#yaver-fb-vibe');
      const vibeRepairBtnEl = overlay.querySelector<HTMLButtonElement>('#yaver-fb-vibe-repair');
      if (!gate || !vibePromptEl || !vibeBtnEl || !vibeRepairBtnEl) return;
      const disable = (reason: string, guidance?: string, opts?: { dashboardLink?: boolean }) => {
        const text = guidance && guidance.trim() ? `${reason} ${guidance}` : reason;
        gate.innerHTML = opts?.dashboardLink
          ? `${escapeHtml(text)} <a class="yvr-fb-inline-link" href="${escapeHtml(dashboardUrl)}" target="_blank" rel="noopener noreferrer">Open your dashboard</a>.`
          : escapeHtml(text);
        gate.style.display = 'block';
        vibeRepairBtnEl.style.display = opts?.dashboardLink ? 'none' : 'block';
        vibePromptEl.disabled = true;
        vibePromptEl.style.opacity = '0.5';
        vibeBtnEl.disabled = true;
        vibeBtnEl.style.opacity = '0.5';
        vibeBtnEl.style.cursor = 'not-allowed';
      };
      const enable = () => {
        gate.textContent = '';
        gate.style.display = 'none';
        vibeRepairBtnEl.style.display = 'none';
        vibePromptEl.disabled = false;
        vibePromptEl.style.opacity = '';
        vibeBtnEl.disabled = false;
        vibeBtnEl.style.opacity = '';
        vibeBtnEl.style.cursor = '';
      };
      try {
        const eligibility = await YaverFeedback.getVibingEligibility();
        if (eligibility.canVibe) {
          syncVibingOnboarding?.(eligibility);
          enable();
        } else {
          const noProjectAccess =
            (eligibility.reason || '').toLowerCase().includes('not allowed for this project');
          syncVibingOnboarding?.(eligibility);
          disable(
            eligibility.reason ?? 'Finish setup before starting a vibing task.',
            eligibility.needsRunnerAuth
              ? 'Start with agent sign-in.'
              : eligibility.guidance,
            noProjectAccess ? { dashboardLink: true } : undefined,
          );
        }
      } catch (err) {
        // Couldn't reach the machine at all — that's the "no access"
        // case the user called out. Gate rather than offer a broken CTA.
        const msg = err instanceof Error ? err.message : String(err);
        disable(
          'Vibing needs a reachable machine.',
          `Sign in to an agent first. (${msg})`,
        );
        syncVibingOnboarding?.(undefined, msg);
      }
    };

    const platformDisplay = (platform: string | undefined): string => {
      const p = String(platform || '').toLowerCase();
      if (p === 'darwin' || p === 'macos') return 'macOS';
      if (p === 'linux') return 'Linux';
      if (p === 'windows') return 'Windows';
      if (p === 'android') return 'Android';
      if (p === 'ios') return 'iOS';
      return platform || 'unknown';
    };
    const refreshMachinePill = async () => {
      const nameEl = overlay.querySelector<HTMLElement>('#yaver-fb-machine-pill-name');
      const metaEl = overlay.querySelector<HTMLElement>('#yaver-fb-machine-pill-meta');
      if (!nameEl || !metaEl) return;
      const cfg = YaverFeedback.config;
      if (!cfg?.preferredDeviceId && !cfg?.agentUrl) {
        nameEl.textContent = 'No machine selected';
        metaEl.textContent = 'Pick one to continue.';
        return;
      }
      // Start with an optimistic label from whatever we can learn via
      // Convex /devices/list (only works when the user has a full session
      // token; SDK tokens return 401 and we fall through).
      let humanName: string | null = null;
      let platform: string | null = null;
      let rowIsOnline: boolean | null = null;
      let rowNeedsAuth = false;
      let rowRunnerDown = false;
      try {
        const devices = await YaverFeedback.listAvailableDevices();
        const selected = devices.find((d) => d.deviceId === cfg.preferredDeviceId);
        if (selected) {
          const nm = selected.name;
          humanName = nm && !/^[0-9a-f-]{36}$/i.test(nm) ? nm : null;
          platform = selected.platform ?? null;
          rowIsOnline = Boolean((selected as any).isOnline);
          rowNeedsAuth = Boolean((selected as any).needsAuth);
          rowRunnerDown = Boolean((selected as any).runnerDown);
        }
      } catch {
        // ignore — fall through to the agent-direct probe below
      }
      // /health is public on the agent (no auth needed), so we can always
      // populate a readable hostname + platform even when the user is
      // only holding an SDK token and /devices/list is not authorized.
      if ((!humanName || !platform) && YaverFeedback.client) {
        try {
          const info = await YaverFeedback.client.info();
          if (info) {
            if (!humanName && info.hostname) humanName = info.hostname;
            if (!platform && info.platform) platform = info.platform;
          }
        } catch {
          // best effort
        }
      }
      nameEl.textContent = humanName ?? 'Unnamed machine';
      // Agent status chip.
      //   - Running & authed  → "Ready"
      //   - needsAuth         → "Needs re-auth"
      //   - runnerDown        → "Runner issue — click to fix"
      //   - /health failed    → "Offline"
      //   - unknown (SDK-token view, no Convex data) → "Agent reachable" (we
      //     just called /info successfully, so we know it's alive).
      let status: string;
      if (rowIsOnline === false) status = 'Offline';
      else if (rowNeedsAuth) status = 'Needs re-auth';
      else if (rowRunnerDown) status = 'Runner issue — click to fix';
      else if (rowIsOnline === true) status = 'Ready';
      else status = humanName ? 'Agent reachable' : 'Connecting…';
      metaEl.textContent = platform ? `${platformDisplay(platform)} · ${status}` : status;
    };

      const setView = (next: 'machine' | 'git' | 'actions') => {
      if (next === 'machine') renderMachineView();
      else if (next === 'git') renderGitSetupView();
      else renderActionsView();
    };

    const decideInitialView = (): 'machine' | 'git' | 'actions' => {
      const cfg = YaverFeedback.config;
      if (!cfg) return 'machine';
      return cfg.agentUrl && cfg.preferredDeviceId ? 'git' : 'machine';
    };

    const statusListener = ((event: Event) => {
      const detail = (event as CustomEvent<FeedbackStatusUpdate>).detail;
      if (!detail) return;
      setStatus(detail.message || 'Working…', detail.progress);
    }) as EventListener;
    window.addEventListener('yaver-feedback:status', statusListener);

    refreshLastReport();
    void refreshAuthStrip();
    setView(decideInitialView());
  }

  // --- Private helpers ---

  private static createFloatingButton(position: string): void {
    const btn = document.createElement('div');
    btn.id = 'yaver-feedback-btn';
    const positions: Record<string, string> = {
      'bottom-right': 'bottom:20px;right:20px;',
      'bottom-left': 'bottom:20px;left:20px;',
      'top-right': 'top:20px;right:20px;',
      'top-left': 'top:20px;left:20px;',
    };
    btn.style.cssText = `
      position:fixed;${positions[position] || positions['bottom-right']}
      width:44px;height:44px;border-radius:50%;
      background:#6366f1;color:white;
      display:flex;align-items:center;justify-content:center;
      cursor:pointer;z-index:99999;font-size:18px;font-weight:bold;
      box-shadow:0 4px 12px rgba(99,102,241,0.4);
      transition:transform 0.2s;
    `;
    btn.textContent = 'Y';
    btn.title = 'Yaver Feedback — report a bug';
    btn.onmouseenter = () => { btn.style.transform = 'scale(1.1)'; };
    btn.onmouseleave = () => { btn.style.transform = 'scale(1)'; };
    btn.onclick = () => YaverFeedback.startReport();
    document.body.appendChild(btn);
    YaverFeedback.widget = btn;
  }

  private static setupKeyboardShortcut(shortcut: string): void {
    const keys = shortcut.toLowerCase().split('+');
    document.addEventListener('keydown', (e) => {
      const match =
        (!keys.includes('ctrl') || e.ctrlKey) &&
        (!keys.includes('shift') || e.shiftKey) &&
        (!keys.includes('alt') || e.altKey) &&
        keys.includes(e.key.toLowerCase());
      if (match) {
        e.preventDefault();
        YaverFeedback.startReport();
      }
    });
  }

  static getLastUploadResult(): FeedbackReportSummary | null {
    return YaverFeedback.lastUploadResult;
  }

  private static async getClient(): Promise<P2PClient> {
    const ready = await YaverFeedback.ensureAgentConnection();
    if (!ready || !YaverFeedback.config?.agentUrl) {
      throw new Error('No Yaver agent connected.');
    }
    YaverFeedback.syncClient();
    if (!YaverFeedback.client) {
      throw new Error('No Yaver agent connected.');
    }
    return YaverFeedback.client;
  }

  private static async ensureAgentConnection(): Promise<boolean> {
    if (!YaverFeedback.config) return false;
    const authed = await YaverFeedback.ensureAuthToken();
    if (!authed) return false;
    if (YaverFeedback.config.agentUrl) {
      YaverFeedback.syncClient();
      YaverFeedback.connectCommandStream();
      return true;
    }
    const picked = await YaverFeedback.ensurePreferredDevice();
    if (!picked) {
      return false;
    }
    const discovered = await YaverFeedback.tryDiscoverSelectedMachine();
    if (!discovered) return false;
    return true;
  }

  private static async listAvailableDevices() {
    if (!YaverFeedback.config?.authToken) return [];
    const devices = await listReachableDevices(YaverFeedback.config.authToken);
    return [...devices.owned, ...devices.shared];
  }

  private static async captureScreenshotBlob(opts: {
    overlay: HTMLElement;
    annotation?: string;
  }): Promise<boolean> {
    const overlay = opts.overlay;
    const previousVisibility = overlay.style.visibility;
    overlay.style.visibility = 'hidden';
    await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    try {
      const blob = await YaverFeedback.renderDocumentToBlob();
      if (!blob) return false;
      YaverFeedback.screenshots.push(blob);
      const elapsed = (Date.now() - YaverFeedback.startTime) / 1000;
      YaverFeedback.timeline.push({
        time: elapsed,
        type: 'screenshot',
        text: opts.annotation || `Screenshot at ${elapsed.toFixed(1)}s`,
      });
      if (opts.annotation) {
        YaverFeedback.timeline.push({
          time: elapsed,
          type: 'annotation',
          text: opts.annotation,
        });
      }
      return true;
    } finally {
      overlay.style.visibility = previousVisibility;
    }
  }

  private static async renderDocumentToBlob(): Promise<Blob | null> {
    const docEl = document.documentElement;
    const width = Math.max(
      docEl.scrollWidth,
      document.body?.scrollWidth ?? 0,
      window.innerWidth,
    );
    const height = Math.max(
      docEl.scrollHeight,
      document.body?.scrollHeight ?? 0,
      window.innerHeight,
    );
    const clone = docEl.cloneNode(true) as HTMLElement;
    clone.querySelectorAll('#yaver-feedback-overlay, #yaver-feedback-btn').forEach((node) =>
      node.remove(),
    );
    clone.setAttribute('xmlns', 'http://www.w3.org/1999/xhtml');
    const serialized = new XMLSerializer().serializeToString(clone);
    const svg = `
      <svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}">
        <foreignObject width="100%" height="100%">${serialized}</foreignObject>
      </svg>
    `;
    const svgBlob = new Blob([svg], { type: 'image/svg+xml;charset=utf-8' });
    const svgUrl = URL.createObjectURL(svgBlob);
    try {
      const img = new Image();
      await new Promise<void>((resolve, reject) => {
        img.onload = () => resolve();
        img.onerror = () => reject(new Error('Unable to render page screenshot.'));
        img.src = svgUrl;
      });
      const canvas = document.createElement('canvas');
      canvas.width = width;
      canvas.height = height;
      const ctx = canvas.getContext('2d');
      if (!ctx) return null;
      ctx.drawImage(img, 0, 0);
      return await new Promise<Blob | null>((resolve) =>
        canvas.toBlob((blob) => resolve(blob), 'image/png'),
      );
    } finally {
      URL.revokeObjectURL(svgUrl);
    }
  }

  private static syncClient(): void {
    if (!YaverFeedback.config?.agentUrl) {
      YaverFeedback.client = null;
      return;
    }
    if (!YaverFeedback.client) {
      YaverFeedback.client = new P2PClient(
        YaverFeedback.config.agentUrl,
        YaverFeedback.config.authToken ?? '',
        YaverFeedback.config.relayPassword ?? '',
      );
      return;
    }
    YaverFeedback.client.setBaseUrl(YaverFeedback.config.agentUrl);
    YaverFeedback.client.setAuthToken(YaverFeedback.config.authToken ?? '');
    YaverFeedback.client.setRelayPassword(YaverFeedback.config.relayPassword ?? '');
  }

  private static connectCommandStream(): void {
    if (!YaverFeedback.config?.agentUrl || !YaverFeedback.config?.authToken) return;
    if (YaverFeedback.commandAbortController) return;
    YaverFeedback.syncClient();
    if (!YaverFeedback.client) return;
    const controller = new AbortController();
    YaverFeedback.commandAbortController = controller;
    void YaverFeedback.client
      .connectCommandStream(
        (command) => YaverFeedback.handleAgentCommand(command),
        {
          deviceId: YaverFeedback.getDeviceId(),
          platform: 'web',
          appName: YaverFeedback.config?.appName,
          signal: controller.signal,
        },
      )
      .catch(() => {
        // reconnect handled below
      })
      .finally(() => {
        if (YaverFeedback.commandAbortController === controller) {
          YaverFeedback.commandAbortController = null;
        }
        if (YaverFeedback.config?.enabled !== false) {
          YaverFeedback.scheduleCommandReconnect();
        }
      });
  }

  private static scheduleCommandReconnect(): void {
    if (YaverFeedback.commandReconnectTimer) return;
    YaverFeedback.commandReconnectTimer = setTimeout(() => {
      YaverFeedback.commandReconnectTimer = null;
      YaverFeedback.connectCommandStream();
    }, 5000);
  }

  private static handleAgentCommand(command: AgentCommand): void {
    if (command.command === 'reload') {
      void YaverFeedback.switchToRemoteDevServerIfNeeded().then((redirected) => {
        if (redirected) return;
        if (YaverFeedback.config?.onReload) {
          YaverFeedback.config.onReload();
        } else {
          window.location.reload();
        }
      });
      return;
    }
    if (command.command === 'reload_bundle') {
      const bundleUrl =
        typeof command.data?.bundleUrl === 'string' ? command.data.bundleUrl : undefined;
      const assetsUrl =
        typeof command.data?.assetsUrl === 'string' ? command.data.assetsUrl : undefined;
      void YaverFeedback.switchToRemoteDevServerIfNeeded().then((redirected) => {
        if (redirected) return;
        if (YaverFeedback.config?.onReloadBundle) {
          YaverFeedback.config.onReloadBundle(bundleUrl, assetsUrl);
        } else {
          window.location.reload();
        }
      });
      return;
    }
    if (command.command === 'status') {
      const status: FeedbackStatusUpdate = {
        message:
          typeof command.data?.message === 'string' ? command.data.message : '',
        phase:
          typeof command.data?.phase === 'string' ? command.data.phase : undefined,
        progress:
          typeof command.data?.progress === 'number' ? command.data.progress : undefined,
        at: Date.now(),
      };
      YaverFeedback.config?.onStatus?.(status);
      window.dispatchEvent(new CustomEvent('yaver-feedback:status', { detail: status }));
    }
  }

  private static getDeviceId(): string {
    if (YaverFeedback.deviceId) return YaverFeedback.deviceId;
    try {
      const key = 'yaver_feedback_web_device_id';
      const existing = localStorage.getItem(key);
      if (existing) {
        YaverFeedback.deviceId = existing;
        return existing;
      }
      const generated = `web-${Math.random().toString(36).slice(2, 10)}`;
      localStorage.setItem(key, generated);
      YaverFeedback.deviceId = generated;
      return generated;
    } catch {
      const fallback = `web-${Math.random().toString(36).slice(2, 10)}`;
      YaverFeedback.deviceId = fallback;
      return fallback;
    }
  }

  private static projectIdentity(): {
    projectName?: string;
    projectPath?: string;
    bundleId?: string;
  } {
    return {
      projectName: YaverFeedback.config?.projectName || YaverFeedback.config?.appName,
      projectPath: YaverFeedback.config?.projectPath,
      bundleId:
        typeof document !== 'undefined'
          ? document.querySelector<HTMLMetaElement>('meta[name="application-name"]')?.content
          : undefined,
    };
  }

  private static async switchToRemoteDevServerIfNeeded(): Promise<boolean> {
    if (typeof window === 'undefined') return false;
    const client = await YaverFeedback.getClient();
    const status = await client.getDevServerStatus();
    if (!status?.running || !status.port) return false;
    if (status.framework !== 'vite' && status.framework !== 'nextjs' && status.framework !== 'flutter') {
      return false;
    }
    const base = status.directUrl || YaverFeedback.config?.agentUrl;
    if (!base) return false;
    const agent = new URL(base);
    const target = new URL(window.location.href);
    target.protocol = agent.protocol === 'https:' ? 'https:' : 'http:';
    target.hostname = agent.hostname;
    target.port = String(status.port);
    if (target.href === window.location.href) return false;
    window.location.href = target.toString();
    return true;
  }

  private static injectReportStyles(): void {
    if (YaverFeedback.reportStyleInjected || typeof document === 'undefined') return;
    const style = document.createElement('style');
    style.id = 'yaver-feedback-report-style';
    style.textContent = `
      .yvr-fb-shell {
        position: fixed; inset: 0; z-index: 99998;
        display: flex; align-items: center; justify-content: center;
        background: rgba(2, 6, 23, 0.62); backdrop-filter: blur(8px); padding: 16px;
      }
      .yvr-fb-card {
        width: min(420px, 100%);
        max-height: min(640px, calc(100vh - 32px));
        overflow: auto;
        background: #0f172a; color: #e2e8f0;
        border-radius: 14px; border: 1px solid rgba(148, 163, 184, 0.18);
        padding: 18px; box-shadow: 0 24px 60px rgba(15, 23, 42, 0.52);
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      .yvr-fb-header { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
      .yvr-fb-title { margin: 0; font-size: 16px; }
      .yvr-fb-subtitle { margin: 4px 0 0; font-size: 12px; color: #94a3b8; line-height: 1.45; }
      .yvr-fb-close {
        border: none; background: transparent; color: #94a3b8;
        cursor: pointer; font-size: 22px; line-height: 1; padding: 0 4px;
      }

      .yvr-fb-auth-strip {
        display: flex; align-items: center; justify-content: space-between; gap: 10px;
        font-size: 12px; color: #cbd5e1; padding: 4px 2px 10px;
        border-bottom: 1px solid rgba(148, 163, 184, 0.14); margin-bottom: 12px;
      }
      .yvr-fb-auth-strip:empty { display: none; }
      .yvr-fb-auth-strip-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
      .yvr-fb-auth-strip strong { color: #e2e8f0; font-weight: 600; }

      .yvr-fb-link {
        background: none; border: none; color: #818cf8;
        font: inherit; font-size: 12px; font-weight: 600; cursor: pointer; padding: 2px 4px;
      }
      .yvr-fb-link:hover { color: #a5b4fc; }
      .yvr-fb-link:disabled { opacity: 0.5; cursor: not-allowed; }

      .yvr-fb-body { display: grid; gap: 12px; }

      /* Dots — green / yellow / red state indicators */
      .yvr-fb-dot { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
      .yvr-fb-dot-green { background: #22c55e; box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.14); }
      .yvr-fb-dot-yellow { background: #f59e0b; box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.14); }
      .yvr-fb-dot-red { background: #ef4444; box-shadow: 0 0 0 3px rgba(239, 68, 68, 0.14); }

      /* Machine picker view */
      .yvr-fb-devices { display: grid; gap: 10px; }
      .yvr-fb-devices-head {
        display: flex; align-items: center; justify-content: space-between;
        margin-top: 4px;
      }
      .yvr-fb-group-label {
        font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8;
      }
      .yvr-fb-devices-list { display: grid; gap: 8px; }
      .yvr-fb-device-row {
        display: flex; align-items: center; gap: 12px;
        width: 100%; text-align: left;
        background: rgba(255,255,255,0.04);
        border: 1px solid rgba(148, 163, 184, 0.14);
        color: inherit; border-radius: 10px; padding: 11px 12px;
        cursor: pointer; font: inherit; transition: background 0.12s;
      }
      .yvr-fb-device-row:hover:not(:disabled) { background: rgba(255,255,255,0.08); }
      .yvr-fb-device-row:disabled { opacity: 0.55; cursor: not-allowed; }
      .yvr-fb-device-row-selected {
        border-color: rgba(99,102,241,0.5); background: rgba(99,102,241,0.12);
      }
      .yvr-fb-device-row-busy { opacity: 0.6; }
      .yvr-fb-device-text { display: grid; gap: 3px; min-width: 0; flex: 1; }
      .yvr-fb-device-name { font-size: 13px; font-weight: 600; color: #e2e8f0; }
      .yvr-fb-device-meta {
        font-size: 11px; color: #94a3b8; line-height: 1.4;
        overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      }
      .yvr-fb-device-selected-badge {
        font-size: 10px; font-weight: 700; color: #a5b4fc;
        text-transform: uppercase; letter-spacing: 0.04em;
      }
      .yvr-fb-device-loading {
        font-size: 12px; color: #94a3b8; text-align: center; padding: 16px;
      }
      .yvr-fb-empty {
        margin: 0; font-size: 12px; color: #94a3b8;
        padding: 10px 12px; border-radius: 10px;
        border: 1px dashed rgba(148, 163, 184, 0.22);
      }
      .yvr-fb-empty code {
        background: rgba(148, 163, 184, 0.14); padding: 0 4px; border-radius: 4px;
      }
      .yvr-fb-inline-link {
        color: #7dd3fc; text-decoration: underline; text-underline-offset: 2px;
      }
      .yvr-fb-inline-link:hover { color: #bae6fd; }
      .yvr-fb-devices-error { margin: 4px 0 0; font-size: 12px; color: #fca5a5; }

      /* Machine pill (actions view) */
      .yvr-fb-machine-pill {
        display: flex; align-items: center; gap: 12px;
        width: 100%; text-align: left;
        background: rgba(15, 23, 42, 0.78);
        border: 1px solid rgba(148, 163, 184, 0.18);
        color: inherit; border-radius: 10px; padding: 10px 12px;
        cursor: pointer; font: inherit;
      }
      .yvr-fb-machine-pill:hover:not(:disabled) { background: rgba(255,255,255,0.05); }
      .yvr-fb-machine-pill:disabled { opacity: 0.6; cursor: not-allowed; }
      .yvr-fb-machine-pill-text { flex: 1; display: grid; gap: 2px; min-width: 0; }
      .yvr-fb-machine-pill-name { font-size: 13px; font-weight: 600; color: #e2e8f0; }
      .yvr-fb-machine-pill-meta {
        font-size: 11px; color: #94a3b8;
        overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      }

      /* Compact 4-tool action row */
      .yvr-fb-toolbar {
        display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px;
      }
      .yvr-fb-tool {
        display: inline-flex; flex-direction: column; align-items: center;
        justify-content: center; gap: 6px;
        padding: 10px 6px; border-radius: 10px;
        background: rgba(255,255,255,0.04);
        border: 1px solid rgba(148, 163, 184, 0.18);
        color: #e2e8f0;
        cursor: pointer; font: inherit; font-size: 12px; font-weight: 600;
        transition: background 0.12s, border-color 0.12s, color 0.12s;
      }
      .yvr-fb-tool:hover:not(:disabled) {
        background: rgba(255,255,255,0.08); border-color: rgba(148, 163, 184, 0.35);
      }
      .yvr-fb-tool:disabled { opacity: 0.55; cursor: not-allowed; }
      .yvr-fb-tool > svg { flex-shrink: 0; }
      .yvr-fb-tool-screenshot { color: #60a5fa; }
      .yvr-fb-tool-screenshot:hover:not(:disabled) { color: #93c5fd; border-color: rgba(96,165,250,0.5); }
      .yvr-fb-tool-reload { color: #c4b5fd; }
      .yvr-fb-tool-reload:hover:not(:disabled) { color: #ddd6fe; border-color: rgba(196,181,253,0.5); }
      .yvr-fb-tool-commit { color: #34d399; }
      .yvr-fb-tool-commit:hover:not(:disabled) { color: #6ee7b7; border-color: rgba(52,211,153,0.5); }
      .yvr-fb-tool-deploy { color: #fbbf24; }
      .yvr-fb-tool-deploy:hover:not(:disabled) { color: #fcd34d; border-color: rgba(251,191,36,0.5); }

      .yvr-fb-vibe-shell { display: grid; gap: 12px; }
      .yvr-fb-vibe-topline {
        display: flex; gap: 10px; align-items: flex-start; justify-content: space-between;
      }
      .yvr-fb-vibe-topline-copy { display: grid; gap: 4px; min-width: 0; }
      .yvr-fb-vibe-intro {
        margin: 0; font-size: 12px; line-height: 1.45; color: #94a3b8;
      }
      .yvr-fb-runner-summary {
        flex-shrink: 0; max-width: 48%;
        padding: 7px 10px; border-radius: 999px;
        border: 1px solid rgba(96,165,250,0.28);
        background: rgba(59,130,246,0.12); color: #bfdbfe;
        font-size: 11px; font-weight: 600; text-align: right;
      }
      .yvr-fb-vibe-onboarding {
        display: grid; gap: 10px; padding: 12px;
        border-radius: 14px; border: 1px solid rgba(148,163,184,0.16);
        background:
          radial-gradient(circle at top left, rgba(14,165,233,0.08), transparent 40%),
          rgba(15,23,42,0.72);
      }
      .yvr-fb-vibe-steps {
        display: flex; flex-wrap: wrap; gap: 8px;
      }
      .yvr-fb-vibe-step {
        padding: 6px 9px; border-radius: 999px;
        border: 1px solid rgba(148,163,184,0.18);
        background: rgba(15,23,42,0.65); color: #94a3b8;
        font-size: 11px; font-weight: 700; letter-spacing: 0.01em;
      }
      .yvr-fb-vibe-step[data-state="done"] {
        border-color: rgba(52,211,153,0.26); background: rgba(16,185,129,0.1); color: #86efac;
      }
      .yvr-fb-vibe-step[data-state="active"] {
        border-color: rgba(96,165,250,0.32); background: rgba(59,130,246,0.14); color: #dbeafe;
      }
      .yvr-fb-vibe-stage { display: grid; gap: 10px; }
      .yvr-fb-vibe-stage-copy { display: grid; gap: 4px; }
      .yvr-fb-vibe-stage-title {
        font-size: 13px; font-weight: 700; color: #f8fafc;
      }
      .yvr-fb-vibe-stage-text {
        font-size: 12px; line-height: 1.5; color: #cbd5e1;
      }
      .yvr-fb-vibe-stage-meta {
        font-size: 11px; line-height: 1.4; color: #7dd3fc;
        font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      }
      .yvr-fb-vibe-radio-row {
        display: flex; gap: 12px; flex-wrap: wrap;
      }
      .yvr-fb-vibe-radio {
        display: inline-flex; align-items: center; gap: 6px;
        font-size: 12px; color: #cbd5e1; cursor: pointer;
      }
      .yvr-fb-vibe-radio input { accent-color: #38bdf8; }
      /* Single-line variants of yvr-fb-vibe-input. The base class has a
         large min-height tuned for the prompt textarea; type=text/password
         inputs reset it so the bootstrap form lays out cleanly. */
      .yvr-fb-vibe-input[type="text"],
      .yvr-fb-vibe-input[type="password"] {
        min-height: 0;
      }

      .yvr-fb-runner-auth-row {
        display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px;
      }
      .yvr-fb-runner-card {
        display: grid; gap: 4px; text-align: left;
        border-radius: 10px; border: 1px solid rgba(148,163,184,0.22);
        background: rgba(15,23,42,0.6); color: #cbd5e1;
        padding: 10px; cursor: pointer; font: inherit;
      }
      .yvr-fb-runner-card:hover:not(:disabled) {
        background: rgba(30,41,59,0.8); border-color: rgba(96,165,250,0.35);
      }
      .yvr-fb-runner-card:disabled { opacity: 0.6; cursor: not-allowed; }
      .yvr-fb-runner-card-kicker {
        font-size: 10px; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.05em;
      }
      .yvr-fb-runner-card-title {
        font-size: 13px; font-weight: 600; color: #f1f5f9;
      }
      .yvr-fb-runner-card-meta {
        font-size: 11px; line-height: 1.4; color: #94a3b8;
      }
      .yvr-fb-runner-card-action {
        font-size: 11px; font-weight: 700; color: #7dd3fc;
      }

      /* Vibing textarea + "Start Vibing Task" + the send-report full-width action */
      .yvr-fb-vibe-block { display: grid; gap: 10px; }
      .yvr-fb-vibe-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8; }
      .yvr-fb-vibe-gate {
        padding: 10px 12px; border-radius: 10px;
        border: 1px solid rgba(245,158,11,0.28);
        background: rgba(245,158,11,0.08); color: #fcd34d;
        font-size: 12px; line-height: 1.45;
      }
      .yvr-fb-vibe-input {
        min-height: 160px; resize: vertical;
        border-radius: 10px; border: 1px solid rgba(148, 163, 184, 0.18);
        background: rgba(15, 23, 42, 0.78); color: inherit;
        padding: 10px 12px; font: inherit; box-sizing: border-box;
      }
      .yvr-fb-action {
        border: none; border-radius: 10px; padding: 11px 12px; color: white;
        cursor: pointer; font: inherit; font-size: 13px; font-weight: 600;
      }
      .yvr-fb-action:disabled { opacity: 0.6; cursor: not-allowed; }
      .yvr-fb-action-secondary {
        background: rgba(30,41,59,0.92); border: 1px solid rgba(148,163,184,0.2);
      }
      .yvr-fb-action-send { background: #16a34a; }
      .yvr-fb-action-vibe { background: #0891b2; }

      .yvr-fb-progress-track {
        margin-top: 12px; width: 100%; height: 8px;
        background: rgba(148, 163, 184, 0.16); border-radius: 999px; overflow: hidden;
      }
      .yvr-fb-progress-fill {
        height: 100%; width: 0%; background: linear-gradient(90deg, #38bdf8 0%, #22c55e 100%);
      }
      .yvr-fb-status, .yvr-fb-last-report { margin: 10px 0 0; font-size: 12px; line-height: 1.45; color: #cbd5e1; }
      .yvr-fb-last-report { color: #94a3b8; }

      @media (max-width: 640px) {
        .yvr-fb-vibe-topline { grid-template-columns: 1fr; display: grid; }
        .yvr-fb-runner-summary { max-width: none; text-align: left; }
        .yvr-fb-runner-auth-row { grid-template-columns: 1fr; }
        .yvr-fb-vibe-input { min-height: 140px; }
      }
    `;
    document.head.appendChild(style);
    YaverFeedback.reportStyleInjected = true;
  }
}
