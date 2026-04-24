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
} from './auth';
import { openLoginModal } from './LoginModal';
import { openDevicePickerModal } from './DevicePickerModal';
import { P2PClient } from './P2PClient';

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
      ? new P2PClient(config.agentUrl, config.authToken ?? '')
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
    if (typeof process !== 'undefined' && process.env?.NODE_ENV) {
      return process.env.NODE_ENV === 'development';
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

  /** Manually trigger the feedback report UI. */
  static startReport(): void {
    void YaverFeedback.launchInteractiveReport();
  }

  private static async launchInteractiveReport(): Promise<void> {
    const ready = await YaverFeedback.prepareInteractiveFlow();
    if (!ready) return;
    YaverFeedback.openReportOverlay();
  }

  private static async prepareInteractiveFlow(opts: {
    forceMachinePicker?: boolean;
  } = {}): Promise<boolean> {
    if (!YaverFeedback.config) return false;
    const authed = await YaverFeedback.ensureAuthToken();
    if (!authed) return false;

    // Skip the device picker if we already know which agent to talk to —
    // either init() auto-discovered one (local `yaver serve` flow) or the
    // host passed `agentUrl` / `preferredDeviceId` directly. Picker is
    // still shown on explicit "Change machine" (forceMachinePicker).
    const needsPicker =
      opts.forceMachinePicker === true ||
      (!YaverFeedback.config.agentUrl && !YaverFeedback.config.preferredDeviceId);
    if (needsPicker) {
      const deviceReady = await YaverFeedback.ensurePreferredDevice({
        forcePicker: opts.forceMachinePicker === true,
      });
      if (!deviceReady) return false;
    }

    if (!YaverFeedback.config.agentUrl && YaverFeedback.config.preferredDeviceId) {
      await YaverFeedback.tryDiscoverSelectedMachine();
    } else {
      YaverFeedback.syncClient();
      YaverFeedback.connectCommandStream();
    }
    return true;
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
              <p class="yvr-fb-subtitle">Report the bug, reload the app, or send the next vibing task to the selected machine.</p>
            </div>
            <button id="yaver-fb-close" class="yvr-fb-close" type="button" aria-label="Close">×</button>
          </div>

          <div class="yvr-fb-auth">
            <div class="yvr-fb-auth-info">
              <div class="yvr-fb-auth-top">
                <span class="yvr-fb-auth-label">Yaver Account</span>
                <span id="yaver-fb-auth-status" class="yvr-fb-auth-status yvr-fb-auth-status-idle">Checking…</span>
              </div>
              <div id="yaver-fb-auth-user" class="yvr-fb-auth-user">&nbsp;</div>
            </div>
            <button id="yaver-fb-auth-action" class="yvr-fb-auth-action" type="button" data-action="sign-in">Sign In</button>
          </div>

          <button id="yaver-fb-machine" class="yvr-fb-machine" type="button">
            <div class="yvr-fb-machine-top">
              <span class="yvr-fb-machine-label">Selected Machine</span>
              <span id="yaver-fb-machine-action" class="yvr-fb-machine-action">Change</span>
            </div>
            <div id="yaver-fb-machine-name" class="yvr-fb-machine-name">Checking connection…</div>
            <div id="yaver-fb-machine-meta" class="yvr-fb-machine-meta">Sign in and pick a machine to unlock reload and vibing.</div>
          </button>

          <div class="yvr-fb-actions">
            <button id="yaver-fb-record" class="yvr-fb-action yvr-fb-action-record" type="button">Start Recording</button>
            <button id="yaver-fb-screenshot" class="yvr-fb-action yvr-fb-action-screenshot" type="button">Screenshot Note</button>
            <button id="yaver-fb-reload" class="yvr-fb-action yvr-fb-action-reload" type="button">Hot Reload</button>
            <button id="yaver-fb-send" class="yvr-fb-action yvr-fb-action-send" type="button" style="display:none;">Stop & Send Report</button>
          </div>

          <div class="yvr-fb-vibe-block">
            <label class="yvr-fb-vibe-label" for="yaver-fb-vibe-prompt">Vibing</label>
            <textarea id="yaver-fb-vibe-prompt" class="yvr-fb-vibe-input" placeholder="Describe what Yaver should work on next..."></textarea>
            <button id="yaver-fb-vibe" class="yvr-fb-action yvr-fb-action-vibe" type="button">Start Vibing Task</button>
          </div>

          <div id="yaver-fb-progress-track" class="yvr-fb-progress-track" style="display:none;">
            <div id="yaver-fb-progress-fill" class="yvr-fb-progress-fill"></div>
          </div>
          <p id="yaver-fb-status" class="yvr-fb-status"></p>
          <p id="yaver-fb-last-report" class="yvr-fb-last-report"></p>
          <button id="yaver-fb-cancel" class="yvr-fb-cancel" type="button">Cancel</button>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);

    const recordBtn = document.getElementById('yaver-fb-record')!;
    const sendBtn = document.getElementById('yaver-fb-send')!;
    const screenshotBtn = document.getElementById('yaver-fb-screenshot')!;
    const reloadBtn = document.getElementById('yaver-fb-reload')!;
    const machineBtn = document.getElementById('yaver-fb-machine')!;
    const machineName = document.getElementById('yaver-fb-machine-name')!;
    const machineMeta = document.getElementById('yaver-fb-machine-meta')!;
    const vibePrompt = document.getElementById('yaver-fb-vibe-prompt') as HTMLTextAreaElement;
    const vibeBtn = document.getElementById('yaver-fb-vibe')!;
    const cancelBtn = document.getElementById('yaver-fb-cancel')!;
    const closeBtn = document.getElementById('yaver-fb-close')!;
    const status = document.getElementById('yaver-fb-status')!;
    const lastReport = document.getElementById('yaver-fb-last-report')!;
    const progressTrack = document.getElementById('yaver-fb-progress-track')!;
    const progressFill = document.getElementById('yaver-fb-progress-fill')!;
    const authStatus = document.getElementById('yaver-fb-auth-status')!;
    const authUser = document.getElementById('yaver-fb-auth-user')!;
    const authAction = document.getElementById('yaver-fb-auth-action') as HTMLButtonElement;

    let busy = false;
    const setBusy = (nextBusy: boolean) => {
      busy = nextBusy;
      [recordBtn, sendBtn, screenshotBtn, reloadBtn, vibeBtn, machineBtn, authAction].forEach((el) => {
        (el as HTMLButtonElement).disabled = nextBusy;
      });
      vibePrompt.disabled = nextBusy;
    };
    const setAuthStatusClass = (kind: 'idle' | 'in' | 'out') => {
      authStatus.classList.remove(
        'yvr-fb-auth-status-idle',
        'yvr-fb-auth-status-in',
        'yvr-fb-auth-status-out',
      );
      authStatus.classList.add(`yvr-fb-auth-status-${kind}`);
    };
    const refreshAuthCard = async () => {
      const token = YaverFeedback.config?.authToken;
      if (!token) {
        authStatus.textContent = 'Not signed in';
        setAuthStatusClass('out');
        authUser.textContent = 'Sign in to pick a machine and start vibing.';
        authAction.textContent = 'Sign In';
        authAction.dataset.action = 'sign-in';
        return;
      }
      let user = getCachedUser();
      if (!user) {
        authStatus.textContent = 'Verifying…';
        setAuthStatusClass('idle');
        authUser.textContent = ' ';
        user = await validateToken(token);
        if (user) saveCachedUser(user);
      }
      if (!user) {
        authStatus.textContent = 'Sign-in expired';
        setAuthStatusClass('out');
        authUser.textContent = 'Your session expired. Sign in again to continue.';
        authAction.textContent = 'Sign In';
        authAction.dataset.action = 'sign-in';
        return;
      }
      authStatus.textContent = 'Signed in';
      setAuthStatusClass('in');
      authUser.textContent = user.name && user.name !== user.email
        ? `${user.email} (${user.name})`
        : user.email;
      authAction.textContent = 'Sign Out';
      authAction.dataset.action = 'sign-out';
    };
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
    const refreshMachineCard = async () => {
      machineName.textContent = 'Checking connection…';
      machineMeta.textContent = 'Sign in and pick a machine to unlock vibing, reload, and screenshots.';
      try {
        if (!YaverFeedback.config?.authToken) {
          machineName.textContent = 'Sign in required';
          machineMeta.textContent =
            'Use your Yaver account to connect this browser to one of your machines.';
          return;
        }
        const devices = await YaverFeedback.listAvailableDevices();
        const selected =
          devices.find(
            (device) => device.deviceId === YaverFeedback.config?.preferredDeviceId,
          ) ?? null;

        if (!selected) {
          machineName.textContent = 'No machine selected';
          machineMeta.textContent =
            'Pick one of your reachable Yaver machines before sending feedback.';
          return;
        }

        machineName.textContent = selected.name || selected.deviceId;
        if (!selected.isOnline) {
          machineMeta.textContent = 'Offline. Start `yaver serve` on the selected machine.';
          return;
        }
        if (selected.needsAuth) {
          machineMeta.textContent =
            'Needs auth. Re-auth or re-pair this machine before using feedback actions.';
          return;
        }
        if (selected.runnerDown) {
          machineMeta.textContent =
            'Machine online, but the coding runner is down.';
          return;
        }

        const discovered = await YaverFeedback.tryDiscoverSelectedMachine();
        if (!discovered || !YaverFeedback.config?.agentUrl) {
          machineMeta.textContent =
            'Machine selected, but the agent URL could not be resolved right now.';
          return;
        }

        const client = YaverFeedback.client;
        const info = client ? await client.info() : null;
        machineMeta.textContent = info
          ? `${info.platform} • ${info.version} • ${YaverFeedback.config.agentUrl}`
          : YaverFeedback.config.agentUrl;
      } catch (err) {
        machineName.textContent = 'Connection needs attention';
        machineMeta.textContent = err instanceof Error ? err.message : 'Unable to inspect the selected machine.';
      }
    };
    const statusListener = ((event: Event) => {
      const detail = (event as CustomEvent<FeedbackStatusUpdate>).detail;
      if (!detail) return;
      setStatus(detail.message || 'Working…', detail.progress);
    }) as EventListener;
    window.addEventListener('yaver-feedback:status', statusListener);
    refreshLastReport();
    void refreshAuthCard();
    void refreshMachineCard();

    authAction.onclick = async () => {
      if (busy) return;
      const action = authAction.dataset.action;
      if (action === 'sign-in') {
        setBusy(true);
        try {
          const token = await openLoginModal();
          if (YaverFeedback.config) YaverFeedback.config.authToken = token;
          const u = await validateToken(token);
          if (u) saveCachedUser(u);
          YaverFeedback.syncClient();
          YaverFeedback.connectCommandStream();
          setStatus('Signed in. Pick a machine to start vibing.');
        } catch {
          setStatus('Sign-in cancelled.');
        } finally {
          setBusy(false);
          await refreshAuthCard();
          await refreshMachineCard();
        }
      } else if (action === 'sign-out') {
        setBusy(true);
        try {
          await YaverFeedback.signOut();
          setStatus('Signed out.');
        } finally {
          setBusy(false);
          await refreshAuthCard();
          await refreshMachineCard();
        }
      }
    };

    recordBtn.onclick = async () => {
      setBusy(true);
      await YaverFeedback.startRecording();
      recordBtn.style.display = 'none';
      sendBtn.style.display = 'block';
      setStatus('Recording… narrate the bug while you move through the broken flow.');
      setBusy(false);
    };

    screenshotBtn.onclick = async () => {
      const note = prompt('Describe this bug (optional):') || '';
      setBusy(true);
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
        setBusy(false);
      }
    };

    reloadBtn.onclick = async () => {
      setBusy(true);
      setStatus('Requesting reload…');
      try {
        const ack = await YaverFeedback.reloadApp('dev');
        setStatus(ack.message);
      } catch (err) {
        setStatus(err instanceof Error ? err.message : 'Reload failed.');
      } finally {
        setBusy(false);
      }
    };

    vibeBtn.onclick = async () => {
      const promptText = vibePrompt.value.trim();
      if (!promptText) {
        setStatus('Enter a vibing prompt first.');
        return;
      }
      setBusy(true);
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
        setBusy(false);
      }
    };

    sendBtn.onclick = async () => {
      setBusy(true);
      setStatus('Sending report…');
      const id = await YaverFeedback.stopAndSend();
      if (id) {
        const changeSet = YaverFeedback.lastUploadResult?.changeSet;
        if (changeSet?.candidateLabel) {
          setStatus(`Report sent: ${id} • ${changeSet.candidateLabel}`);
        } else {
          setStatus(`Report sent: ${id}`);
        }
        refreshLastReport();
        setTimeout(() => overlay.remove(), 2000);
      } else {
        setStatus('Failed to send. Check console.');
        setBusy(false);
      }
    };

    machineBtn.onclick = async () => {
      setBusy(true);
      setStatus('Opening machine picker…');
      try {
        const ready = await YaverFeedback.prepareInteractiveFlow({ forceMachinePicker: true });
        if (ready) {
          setStatus('Connected to selected machine.');
        } else {
          setStatus('Machine selection cancelled.');
        }
      } catch (err) {
        setStatus(err instanceof Error ? err.message : 'Unable to change machine.');
      } finally {
        await refreshMachineCard();
        setBusy(false);
      }
    };

    cancelBtn.onclick = () => {
      if (YaverFeedback.recording) {
        YaverFeedback.mediaRecorder?.stop();
        YaverFeedback.audioRecorder?.stop();
        YaverFeedback.recording = false;
      }
      window.removeEventListener('yaver-feedback:status', statusListener);
      overlay.remove();
    };
    closeBtn.onclick = cancelBtn.onclick;
    overlay.onclick = (event) => {
      if (event.target === overlay) cancelBtn.onclick?.(event as any);
    };
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
      );
      return;
    }
    YaverFeedback.client.setBaseUrl(YaverFeedback.config.agentUrl);
    YaverFeedback.client.setAuthToken(YaverFeedback.config.authToken ?? '');
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
        position: fixed;
        inset: 0;
        z-index: 99998;
        display: flex;
        align-items: center;
        justify-content: center;
        background: rgba(2, 6, 23, 0.62);
        backdrop-filter: blur(8px);
        padding: 16px;
      }
      .yvr-fb-card {
        width: min(420px, 100%);
        background: #0f172a;
        color: #e2e8f0;
        border-radius: 14px;
        border: 1px solid rgba(148, 163, 184, 0.18);
        padding: 18px;
        box-shadow: 0 24px 60px rgba(15, 23, 42, 0.52);
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      .yvr-fb-header { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
      .yvr-fb-title { margin: 0; font-size: 16px; }
      .yvr-fb-subtitle { margin: 4px 0 0; font-size: 12px; color: #94a3b8; line-height: 1.45; }
      .yvr-fb-close {
        border: none; background: transparent; color: #94a3b8; cursor: pointer; font-size: 22px; line-height: 1; padding: 0 4px;
      }
      .yvr-fb-auth {
        display: flex; align-items: center; gap: 10px;
        background: rgba(15, 23, 42, 0.78); border: 1px solid rgba(148, 163, 184, 0.18);
        border-radius: 10px; padding: 10px 12px; margin-bottom: 10px;
      }
      .yvr-fb-auth-info { flex: 1; min-width: 0; }
      .yvr-fb-auth-top { display: flex; align-items: center; gap: 8px; margin-bottom: 4px; }
      .yvr-fb-auth-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8; }
      .yvr-fb-auth-status {
        font-size: 10px; padding: 2px 6px; border-radius: 999px;
        text-transform: uppercase; letter-spacing: 0.04em; font-weight: 600;
      }
      .yvr-fb-auth-status-in { background: rgba(34, 197, 94, 0.18); color: #4ade80; }
      .yvr-fb-auth-status-out { background: rgba(248, 113, 113, 0.18); color: #fca5a5; }
      .yvr-fb-auth-status-idle { background: rgba(148, 163, 184, 0.18); color: #cbd5e1; }
      .yvr-fb-auth-user {
        font-size: 12px; color: #e2e8f0; line-height: 1.4;
        overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      }
      .yvr-fb-auth-action {
        flex-shrink: 0; border: 1px solid rgba(148, 163, 184, 0.35); background: transparent;
        color: #e2e8f0; border-radius: 8px; padding: 6px 12px; font: inherit; font-size: 12px;
        font-weight: 600; cursor: pointer;
      }
      .yvr-fb-auth-action:hover { background: rgba(148, 163, 184, 0.12); }
      .yvr-fb-auth-action:disabled { opacity: 0.5; cursor: not-allowed; }
      .yvr-fb-machine {
        width: 100%; text-align: left; background: rgba(15, 23, 42, 0.78); border: 1px solid rgba(148, 163, 184, 0.18);
        color: inherit; border-radius: 10px; padding: 12px; margin-bottom: 12px; cursor: pointer;
      }
      .yvr-fb-machine-top { display: flex; justify-content: space-between; gap: 12px; margin-bottom: 6px; }
      .yvr-fb-machine-label, .yvr-fb-machine-action { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8; }
      .yvr-fb-machine-name { font-size: 14px; font-weight: 600; margin-bottom: 4px; }
      .yvr-fb-machine-meta { font-size: 12px; color: #94a3b8; line-height: 1.45; word-break: break-word; }
      .yvr-fb-actions, .yvr-fb-vibe-block { display: grid; gap: 8px; }
      .yvr-fb-vibe-block { margin-top: 12px; }
      .yvr-fb-vibe-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; color: #94a3b8; }
      .yvr-fb-vibe-input {
        min-height: 86px; resize: vertical; border-radius: 10px; border: 1px solid rgba(148, 163, 184, 0.18);
        background: rgba(15, 23, 42, 0.78); color: inherit; padding: 10px 12px; font: inherit; box-sizing: border-box;
      }
      .yvr-fb-action, .yvr-fb-cancel {
        border: none; border-radius: 10px; padding: 11px 12px; color: white; cursor: pointer; font: inherit; font-size: 13px; font-weight: 600;
      }
      .yvr-fb-action:disabled, .yvr-fb-machine:disabled { opacity: 0.6; cursor: not-allowed; }
      .yvr-fb-action-record { background: #dc2626; }
      .yvr-fb-action-screenshot { background: #2563eb; }
      .yvr-fb-action-reload { background: #7c3aed; }
      .yvr-fb-action-send { background: #16a34a; }
      .yvr-fb-action-vibe { background: #0891b2; }
      .yvr-fb-progress-track {
        margin-top: 12px; width: 100%; height: 8px; background: rgba(148, 163, 184, 0.16); border-radius: 999px; overflow: hidden;
      }
      .yvr-fb-progress-fill {
        height: 100%; width: 0%; background: linear-gradient(90deg, #38bdf8 0%, #22c55e 100%);
      }
      .yvr-fb-status, .yvr-fb-last-report { margin: 12px 0 0; font-size: 12px; line-height: 1.45; color: #cbd5e1; }
      .yvr-fb-last-report { color: #94a3b8; }
      .yvr-fb-cancel { width: 100%; background: #334155; color: #e2e8f0; margin-top: 12px; }
    `;
    document.head.appendChild(style);
    YaverFeedback.reportStyleInjected = true;
  }
}
