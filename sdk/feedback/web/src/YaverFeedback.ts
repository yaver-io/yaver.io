import type {
  AgentCommand,
  FeedbackBundle,
  FeedbackConfig,
  FeedbackReportSummary,
  FeedbackStatusUpdate,
  TimelineEvent,
  DeviceInfo,
  ReloadAck,
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
    repoFullName?: string;
  }> {
    const client = await YaverFeedback.getClient();
    return client.getVibingEligibility(YaverFeedback.projectIdentity());
  }

  static async vibing(prompt: string): Promise<{ taskId: string }> {
    const client = await YaverFeedback.getClient();
    return client.vibing(prompt, YaverFeedback.projectIdentity());
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
              <h3 class="yvr-fb-title">Yaver Feedback</h3>
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
      subtitle.textContent = 'Step 1 of 2 — Pick the machine to connect to.';
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
          errorEl.textContent = 'No machines found for this account.';
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
              setView('actions');
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

    // ── Step 3 — Actions ────────────────────────────────

    const renderActionsView = () => {
      subtitle.textContent = 'Step 2 of 2 — Record, reload, or start the next vibing task.';
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
          <button id="yaver-fb-record" class="yvr-fb-tool yvr-fb-tool-record" type="button" title="Start recording">
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><circle cx="12" cy="12" r="6" fill="currentColor"/></svg>
            <span>Record</span>
          </button>
          <button id="yaver-fb-screenshot" class="yvr-fb-tool yvr-fb-tool-screenshot" type="button" title="Screenshot + note">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/></svg>
            <span>Screenshot</span>
          </button>
          <button id="yaver-fb-reload" class="yvr-fb-tool yvr-fb-tool-reload" type="button" title="Hot reload">
            <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 4v5h5"/><path d="M20 20v-5h-5"/><path d="M5.5 9A7.5 7.5 0 0 1 19 8.5"/><path d="M18.5 15A7.5 7.5 0 0 1 5 15.5"/></svg>
            <span>Reload</span>
          </button>
        </div>
        <button id="yaver-fb-send" class="yvr-fb-action yvr-fb-action-send" type="button" style="display:none;">Stop &amp; Send Report</button>

        <div class="yvr-fb-runner-auth-row" style="display:flex;gap:6px;margin-top:10px;flex-wrap:wrap;">
          <button id="yaver-fb-signin-codex" type="button" style="flex:1;min-width:140px;padding:8px 10px;border-radius:10px;border:1px solid rgba(148,163,184,0.22);background:rgba(15,23,42,0.6);color:#cbd5e1;font-size:11px;cursor:pointer;text-align:left;">
            <span style="display:block;font-size:10px;color:#94a3b8;text-transform:uppercase;letter-spacing:0.05em;">Remote sign-in</span>
            <span style="display:block;font-weight:600;color:#f1f5f9;">Codex</span>
          </button>
          <button id="yaver-fb-signin-claude" type="button" style="flex:1;min-width:140px;padding:8px 10px;border-radius:10px;border:1px solid rgba(148,163,184,0.22);background:rgba(15,23,42,0.6);color:#cbd5e1;font-size:11px;cursor:pointer;text-align:left;">
            <span style="display:block;font-size:10px;color:#94a3b8;text-transform:uppercase;letter-spacing:0.05em;">Remote sign-in</span>
            <span style="display:block;font-weight:600;color:#f1f5f9;">Claude</span>
          </button>
        </div>

        <div class="yvr-fb-vibe-block">
          <label class="yvr-fb-vibe-label" for="yaver-fb-vibe-prompt">Vibing</label>
          <div id="yaver-fb-vibe-gate" style="display:none;padding:10px 12px;border-radius:10px;border:1px solid rgba(245,158,11,0.3);background:rgba(245,158,11,0.08);color:#fbbf24;font-size:12px;line-height:1.45;margin-bottom:8px;"></div>
          <textarea id="yaver-fb-vibe-prompt" class="yvr-fb-vibe-input" placeholder="Describe what Yaver should work on next..."></textarea>
          <button id="yaver-fb-vibe" class="yvr-fb-action yvr-fb-action-vibe" type="button">Start Vibing Task</button>
        </div>
      `;

      const recordBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-record')!;
      const sendBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-send')!;
      const screenshotBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-screenshot')!;
      const reloadBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-reload')!;
      const vibeBtn = overlay.querySelector<HTMLButtonElement>('#yaver-fb-vibe')!;
      const vibePrompt = overlay.querySelector<HTMLTextAreaElement>('#yaver-fb-vibe-prompt')!;
      const machinePill = overlay.querySelector<HTMLButtonElement>('#yaver-fb-machine-pill')!;
      const signInCodex = overlay.querySelector<HTMLButtonElement>('#yaver-fb-signin-codex');
      const signInClaude = overlay.querySelector<HTMLButtonElement>('#yaver-fb-signin-claude');
      // Refresh vibing gate + machine pill after a sign-in attempt
      // completes (success or cancel) — if auth landed, the gate lifts
      // automatically without making the user close & re-open the
      // feedback widget.
      const afterSignIn = async () => {
        await Promise.all([refreshMachinePill(), refreshVibingGate()]);
      };
      if (signInCodex) signInCodex.onclick = () => {
        YaverFeedback.signInRunner('codex').finally(() => { void afterSignIn(); });
      };
      if (signInClaude) signInClaude.onclick = () => {
        YaverFeedback.signInRunner('claude').finally(() => { void afterSignIn(); });
      };

      const setActionsBusy = (value: boolean) => {
        busy = value;
        [recordBtn, sendBtn, screenshotBtn, reloadBtn, vibeBtn, machinePill].forEach(
          (el) => ((el as HTMLButtonElement).disabled = value),
        );
        vibePrompt.disabled = value;
      };

      machinePill.onclick = () => {
        if (!busy) setView('machine');
      };

      recordBtn.onclick = async () => {
        setActionsBusy(true);
        await YaverFeedback.startRecording();
        recordBtn.style.display = 'none';
        sendBtn.style.display = 'block';
        setStatus('Recording… narrate the bug while you move through the broken flow.');
        setActionsBusy(false);
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
          setStatus(ack.message);
        } catch (err) {
          setStatus(err instanceof Error ? err.message : 'Reload failed.');
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

      sendBtn.onclick = async () => {
        setActionsBusy(true);
        setStatus('Sending report…');
        const id = await YaverFeedback.stopAndSend();
        if (id) {
          const changeSet = YaverFeedback.lastUploadResult?.changeSet;
          setStatus(
            changeSet?.candidateLabel
              ? `Report sent: ${id} • ${changeSet.candidateLabel}`
              : `Report sent: ${id}`,
          );
          refreshLastReport();
          setTimeout(() => cleanup(), 2000);
        } else {
          setStatus('Failed to send. Check console.');
          setActionsBusy(false);
        }
      };

      void refreshMachinePill();
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
      if (!gate || !vibePromptEl || !vibeBtnEl) return;
      const disable = (reason: string, guidance?: string) => {
        gate.textContent = guidance && guidance.trim() ? `${reason} ${guidance}` : reason;
        gate.style.display = 'block';
        vibePromptEl.disabled = true;
        vibePromptEl.style.opacity = '0.5';
        vibeBtnEl.disabled = true;
        vibeBtnEl.style.opacity = '0.5';
        vibeBtnEl.style.cursor = 'not-allowed';
      };
      const enable = () => {
        gate.textContent = '';
        gate.style.display = 'none';
        vibePromptEl.disabled = false;
        vibePromptEl.style.opacity = '';
        vibeBtnEl.disabled = false;
        vibeBtnEl.style.opacity = '';
        vibeBtnEl.style.cursor = '';
      };
      try {
        const eligibility = await YaverFeedback.getVibingEligibility();
        if (eligibility.canVibe) {
          enable();
        } else {
          disable(
            eligibility.reason ?? 'Vibing is not available on this machine.',
            eligibility.guidance,
          );
        }
      } catch (err) {
        // Couldn't reach the machine at all — that's the "no access"
        // case the user called out. Gate rather than offer a broken CTA.
        const msg = err instanceof Error ? err.message : String(err);
        disable(
          'Vibing requires a reachable machine with an authenticated coding agent.',
          `Sign in to Codex or Claude on this machine first. (${msg})`,
        );
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

    const setView = (next: 'machine' | 'actions') => {
      if (next === 'machine') renderMachineView();
      else renderActionsView();
    };

    const decideInitialView = (): 'machine' | 'actions' => {
      const cfg = YaverFeedback.config;
      if (!cfg) return 'machine';
      return cfg.agentUrl && cfg.preferredDeviceId ? 'actions' : 'machine';
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
      if (YaverFeedback.config?.onReload) {
        YaverFeedback.config.onReload();
      } else {
        window.location.reload();
      }
      return;
    }
    if (command.command === 'reload_bundle') {
      const bundleUrl =
        typeof command.data?.bundleUrl === 'string' ? command.data.bundleUrl : undefined;
      const assetsUrl =
        typeof command.data?.assetsUrl === 'string' ? command.data.assetsUrl : undefined;
      if (YaverFeedback.config?.onReloadBundle) {
        YaverFeedback.config.onReloadBundle(bundleUrl, assetsUrl);
      } else {
        window.location.reload();
      }
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
      projectName: YaverFeedback.config?.projectName,
      projectPath: YaverFeedback.config?.projectPath,
      bundleId:
        typeof document !== 'undefined'
          ? document.querySelector<HTMLMetaElement>('meta[name="application-name"]')?.content
          : undefined,
    };
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

      /* Compact 3-tool action row (Record / Screenshot / Reload) */
      .yvr-fb-toolbar {
        display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px;
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
      .yvr-fb-tool-record { color: #f87171; }
      .yvr-fb-tool-record:hover:not(:disabled) { color: #fca5a5; border-color: rgba(248,113,113,0.5); }
      .yvr-fb-tool-screenshot { color: #60a5fa; }
      .yvr-fb-tool-screenshot:hover:not(:disabled) { color: #93c5fd; border-color: rgba(96,165,250,0.5); }
      .yvr-fb-tool-reload { color: #c4b5fd; }
      .yvr-fb-tool-reload:hover:not(:disabled) { color: #ddd6fe; border-color: rgba(196,181,253,0.5); }

      /* Vibing textarea + "Start Vibing Task" + the send-report full-width action */
      .yvr-fb-vibe-block { display: grid; gap: 8px; }
      .yvr-fb-vibe-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8; }
      .yvr-fb-vibe-input {
        min-height: 86px; resize: vertical;
        border-radius: 10px; border: 1px solid rgba(148, 163, 184, 0.18);
        background: rgba(15, 23, 42, 0.78); color: inherit;
        padding: 10px 12px; font: inherit; box-sizing: border-box;
      }
      .yvr-fb-action {
        border: none; border-radius: 10px; padding: 11px 12px; color: white;
        cursor: pointer; font: inherit; font-size: 13px; font-weight: 600;
      }
      .yvr-fb-action:disabled { opacity: 0.6; cursor: not-allowed; }
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
    `;
    document.head.appendChild(style);
    YaverFeedback.reportStyleInjected = true;
  }
}
