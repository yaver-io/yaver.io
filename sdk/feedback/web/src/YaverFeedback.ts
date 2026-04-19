import type { FeedbackConfig, FeedbackBundle, TimelineEvent, DeviceInfo } from './types';
import { YaverDiscovery } from './discovery';
import { getToken as getCachedToken } from './auth';
import { openLoginModal } from './LoginModal';

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
  private static timeline: TimelineEvent[] = [];
  private static startTime = 0;
  private static recording = false;
  private static consoleErrors: string[] = [];
  private static widget: HTMLElement | null = null;

  /** Initialize the feedback SDK. Call once in your app entry point. */
  static async init(config: FeedbackConfig = {}): Promise<void> {
    // Default to enabled in development
    if (config.enabled === undefined) {
      config.enabled = typeof process !== 'undefined'
        ? process.env?.NODE_ENV === 'development'
        : !window.location.hostname.includes('prod');
    }

    if (!config.enabled) return;

    // Hydrate auth token from localStorage if caller didn't pass one.
    if (!config.authToken) {
      const cached = getCachedToken();
      if (cached) config.authToken = cached;
    }

    // Auto-discover agent if no URL provided
    if (!config.agentUrl) {
      const agent = await YaverDiscovery.discover();
      if (agent) {
        config.agentUrl = agent.url;
        console.log(`[yaver-feedback] Connected to ${agent.hostname} (${agent.url})`);
      } else {
        console.warn('[yaver-feedback] No Yaver agent found. Set agentUrl manually or run "yaver serve" on your dev machine.');
      }
    }

    YaverFeedback.config = config;

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
  }

  /** Check if SDK is initialized and enabled. */
  static get isInitialized(): boolean {
    return YaverFeedback.config !== null && YaverFeedback.config.enabled !== false;
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
      },
      video: video.size > 0 ? video : undefined,
      audio: audio.size > 0 ? audio : undefined,
      screenshots: [],
    };

    return YaverFeedback.upload(bundle);
  }

  /** Upload feedback bundle to Yaver agent via multipart POST. */
  static async upload(bundle: FeedbackBundle): Promise<string | null> {
    const agentUrl = YaverFeedback.config?.agentUrl;
    if (!agentUrl) {
      console.error('[yaver-feedback] No agent URL configured');
      return null;
    }

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

      const result = await resp.json();
      console.log(`[yaver-feedback] Report sent: ${result.id}`);
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
      if (YaverFeedback.config) YaverFeedback.config.authToken = token;
      return true;
    } catch {
      return false;
    }
  }

  /** Manually trigger the feedback report UI. */
  static startReport(): void {
    // Auth happens lazily — at upload time, not at trigger time. The user
    // can record + screenshot without a session; we only need a token to
    // POST the bundle to the agent.
    // Create a simple overlay UI
    const overlay = document.createElement('div');
    overlay.id = 'yaver-feedback-overlay';
    overlay.innerHTML = `
      <div style="position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.5);z-index:99998;display:flex;align-items:center;justify-content:center;">
        <div style="background:#1a1a2e;color:#e0e0e0;padding:24px;border-radius:12px;max-width:360px;width:90%;font-family:-apple-system,sans-serif;box-shadow:0 20px 60px rgba(0,0,0,0.5);">
          <h3 style="margin:0 0 16px;font-size:16px;">Yaver Feedback</h3>
          <p style="margin:0 0 16px;font-size:13px;color:#888;">Record your screen and voice to report bugs. The AI agent will fix them.</p>
          <div style="display:flex;flex-direction:column;gap:8px;">
            <button id="yaver-fb-record" style="padding:10px;border:none;border-radius:8px;background:#dc2626;color:white;cursor:pointer;font-size:13px;">Start Recording</button>
            <button id="yaver-fb-screenshot" style="padding:10px;border:none;border-radius:8px;background:#2563eb;color:white;cursor:pointer;font-size:13px;">Take Screenshot</button>
            <button id="yaver-fb-send" style="padding:10px;border:none;border-radius:8px;background:#16a34a;color:white;cursor:pointer;font-size:13px;display:none;">Stop & Send Report</button>
            <button id="yaver-fb-cancel" style="padding:10px;border:none;border-radius:8px;background:#333;color:#888;cursor:pointer;font-size:13px;">Cancel</button>
          </div>
          <p id="yaver-fb-status" style="margin:12px 0 0;font-size:11px;color:#666;"></p>
        </div>
      </div>
    `;
    document.body.appendChild(overlay);

    const recordBtn = document.getElementById('yaver-fb-record')!;
    const sendBtn = document.getElementById('yaver-fb-send')!;
    const screenshotBtn = document.getElementById('yaver-fb-screenshot')!;
    const cancelBtn = document.getElementById('yaver-fb-cancel')!;
    const status = document.getElementById('yaver-fb-status')!;

    recordBtn.onclick = async () => {
      await YaverFeedback.startRecording();
      recordBtn.style.display = 'none';
      sendBtn.style.display = 'block';
      status.textContent = 'Recording... narrate the bugs you see.';
    };

    screenshotBtn.onclick = () => {
      const note = prompt('Describe this bug (optional):') || '';
      YaverFeedback.captureScreenshot(note);
      status.textContent = `Screenshot captured${note ? ': ' + note : ''}`;
    };

    sendBtn.onclick = async () => {
      status.textContent = 'Sending report...';
      const id = await YaverFeedback.stopAndSend();
      if (id) {
        status.textContent = `Report sent! (${id})`;
        setTimeout(() => overlay.remove(), 2000);
      } else {
        status.textContent = 'Failed to send. Check console.';
      }
    };

    cancelBtn.onclick = () => {
      if (YaverFeedback.recording) {
        YaverFeedback.mediaRecorder?.stop();
        YaverFeedback.audioRecorder?.stop();
        YaverFeedback.recording = false;
      }
      overlay.remove();
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
}
