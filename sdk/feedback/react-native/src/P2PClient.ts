import { Platform } from 'react-native';
import {
  CapabilitySnapshot,
  FeedbackBundle,
  IncidentEvent,
  OperationState,
  RunnerBrowserAuthSession,
  TestSession,
  VoiceCapability,
} from './types';

export interface FeedbackEvent {
  type: string;
  timestamp: string;
  data: any;
}

export interface ReloadAck {
  ok: boolean;
  mode: 'dev' | 'bundle';
  acknowledged: boolean;
  message: string;
  nativeChangesDetected?: boolean;
  changeClass?: string;
}

/**
 * Try to resolve `{projectName, bundleId}` for the running app so the
 * agent can map the reload request to a MobileProject in its scan
 * cache. Order: caller-supplied opts → Expo Constants → react-native
 * NativeModules. None of the lookups throw — missing data just means
 * the agent will fall back to its own dev-server resolution.
 */
function resolveAppIdentity(opts?: {
  projectName?: string;
  bundleId?: string;
  projectPath?: string;
}): { projectName?: string; bundleId?: string; projectPath?: string } {
  let projectName = opts?.projectName;
  let bundleId = opts?.bundleId;
  const projectPath = opts?.projectPath;

  if (!projectName || !bundleId) {
    try {
      const Constants = require('expo-constants').default ?? require('expo-constants');
      const cfg = Constants?.expoConfig ?? Constants?.manifest ?? {};
      projectName = projectName || cfg?.name;
      bundleId =
        bundleId ||
        cfg?.ios?.bundleIdentifier ||
        cfg?.android?.package;
    } catch {
      // expo-constants not installed (bare RN). Fall through.
    }
  }

  if (!bundleId) {
    try {
      const { Platform, NativeModules } = require('react-native');
      if (Platform.OS === 'ios') {
        bundleId = NativeModules?.SettingsManager?.settings?.CFBundleIdentifier;
      } else if (Platform.OS === 'android') {
        bundleId = NativeModules?.PlatformConstants?.Package;
      }
    } catch {
      // SettingsManager/PlatformConstants missing on some RN versions.
    }
  }

  const out: { projectName?: string; bundleId?: string; projectPath?: string } = {};
  if (projectName) out.projectName = projectName;
  if (bundleId) out.bundleId = bundleId;
  if (projectPath) out.projectPath = projectPath;
  return out;
}

/**
 * Translate a raw Go-agent error into something a user can act on.
 *
 * The agent surfaces Go's low-level error text verbatim inside JSON,
 * e.g. `Get "http://127.0.0.1:8081/reload": dial tcp 127.0.0.1:8081:
 * connect: connection refused`. That's accurate but unreadable for a
 * phone user and — more importantly — leads them to think the SDK is
 * misconfigured. The real cause is almost always "no dev server
 * running on the host machine".
 */
function friendlyReloadError(status: number, body: string): string {
  // Plain .indexOf instead of regex — short literal tests sidestep
  // a Hermes rope-flatten SIGSEGV we saw on RN 0.81 / iOS 18.3.1
  // when the same body was already being processed by a concurrent
  // SSE reconnect loop.
  const lower = (body || '').toLowerCase();
  const hasRefused =
    lower.indexOf('connection refused') >= 0 ||
    lower.indexOf('econnrefused') >= 0;
  const hasLoopback =
    lower.indexOf('127.0.0.1') >= 0 || lower.indexOf('localhost') >= 0;
  if (hasRefused && hasLoopback) {
    return (
      'No dev server running on your machine. ' +
      'Start Metro with `yaver dev start` or use "Screenshot & Fix" instead.'
    );
  }
  if (
    lower.indexOf('no dev server') >= 0 ||
    lower.indexOf('not running') >= 0
  ) {
    return 'No dev server running on your machine. Start Metro first.';
  }
  if (status === 401 || status === 403) {
    return 'Agent rejected the session — please sign in again.';
  }
  if (status >= 500) {
    return 'Agent hit an internal error while reloading. Check `yaver logs`.';
  }
  return `Reload failed (${status}).`;
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
  /**
   * Shared relay password. Required when baseUrl points through the
   * Yaver managed relay (e.g. https://public.yaver.io/d/<deviceId>) —
   * the relay rejects unauthenticated requests with 401. Attached as
   * X-Relay-Password on every agent request.
   */
  private relayPassword: string;

  constructor(baseUrl: string, authToken: string, relayPassword: string = '') {
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.authToken = authToken;
    this.relayPassword = relayPassword;
  }

  /** Update the base URL (e.g. after re-discovery). */
  setBaseUrl(url: string): void {
    this.baseUrl = url.replace(/\/$/, '');
  }

  /** Update the auth token. */
  setAuthToken(token: string): void {
    this.authToken = token;
  }

  /** Update the relay password (used for managed-relay baseUrls). */
  setRelayPassword(password: string): void {
    this.relayPassword = password;
  }

  /** Merge in Authorization + (optional) X-Relay-Password on top of a header block. */
  private authHeaders(extra: Record<string, string> = {}): Record<string, string> {
    const h: Record<string, string> = { ...extra };
    if (this.authToken) h.Authorization = `Bearer ${this.authToken}`;
    if (this.relayPassword) h['X-Relay-Password'] = this.relayPassword;
    return h;
  }

  /**
   * Start a remote browser-style sign-in for a runner (codex --device-auth
   * / claude auth login --console). Returns a session id; callers poll
   * getRunnerBrowserAuthStatus to surface the verification URL + one-time
   * code. No API keys involved — the CLI writes its own auth.json once
   * the user completes the flow in any browser.
   */
  async startRunnerBrowserAuth(runner: string): Promise<RunnerBrowserAuthSession> {
    const resp = await fetch(`${this.baseUrl}/runner-auth/browser/start`, {
      method: 'POST',
      headers: this.authHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ runner }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`startRunnerBrowserAuth(${runner}) HTTP ${resp.status}: ${text}`);
    }
    const data = await resp.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async getRunnerBrowserAuthStatus(sessionId: string): Promise<RunnerBrowserAuthSession> {
    const url = `${this.baseUrl}/runner-auth/browser/status?id=${encodeURIComponent(sessionId)}`;
    const resp = await fetch(url, { headers: this.authHeaders() });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`getRunnerBrowserAuthStatus HTTP ${resp.status}: ${text}`);
    }
    const data = await resp.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async cancelRunnerBrowserAuth(sessionId: string): Promise<void> {
    const url = `${this.baseUrl}/runner-auth/browser/cancel?id=${encodeURIComponent(sessionId)}`;
    try { await fetch(url, { method: 'POST', headers: this.authHeaders() }); } catch { /* best-effort */ }
  }

  /** Submit the verifier code Anthropic shows on the callback page so
   *  the agent can finalise claude CLI's OAuth handshake. Codex doesn't
   *  use this — its device-auth flow auto-resolves via polling — but
   *  the SDK still exposes it for symmetry with mobile/src/components/
   *  RunnerAuthModal.tsx and the Swift YaverRunnerAuthFlowPane. */
  async submitRunnerBrowserAuthCode(
    sessionId: string,
    code: string,
  ): Promise<RunnerBrowserAuthSession> {
    const url = `${this.baseUrl}/runner-auth/browser/submit-code`;
    const resp = await fetch(url, {
      method: 'POST',
      headers: { ...this.authHeaders(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: sessionId, code }),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => '');
      throw new Error(`submitRunnerBrowserAuthCode HTTP ${resp.status}: ${text}`);
    }
    const data = await resp.json();
    return data.session as RunnerBrowserAuthSession;
  }

  async capabilitySnapshot(): Promise<CapabilitySnapshot | null> {
    try {
      const resp = await fetch(`${this.baseUrl}/capabilities/snapshot`, { headers: this.authHeaders() });
      if (!resp.ok) return null;
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      return (data.snapshot ?? null) as CapabilitySnapshot | null;
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
      const resp = await fetch(url.toString(), { headers: this.authHeaders() });
      if (!resp.ok) return [];
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      return Array.isArray(data.incidents) ? (data.incidents as IncidentEvent[]) : [];
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
      const resp = await fetch(url.toString(), { headers: this.authHeaders() });
      if (!resp.ok) return [];
      const data = await resp.json().catch(() => ({} as Record<string, unknown>));
      return Array.isArray(data.operations) ? (data.operations as OperationState[]) : [];
    } catch {
      return [];
    }
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

    if (bundle.video) {
      formData.append('video', {
        uri: Platform.OS === 'android' ? `file://${bundle.video}` : bundle.video,
        type: 'video/mp4',
        name: 'screen_recording.mp4',
      } as any);
    }

    if (bundle.audio) {
      formData.append('audio', {
        uri: Platform.OS === 'android' ? `file://${bundle.audio}` : bundle.audio,
        type: 'audio/m4a',
        name: 'voice_note.m4a',
      } as any);
    }

    // Use authHeaders() so a relay-routed baseUrl carries
    // X-Relay-Password — without it the relay rejects with 401
    // "invalid relay password" before the agent ever sees the form.
    const response = await fetch(`${this.baseUrl}/feedback`, {
      method: 'POST',
      headers: this.authHeaders(),
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
        headers: this.authHeaders({ 'Content-Type': 'application/json' }),
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
      headers: this.authHeaders({ "Content-Type": "application/json" }),
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
      headers: this.authHeaders(),
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

  /**
   * Trigger a reload of the third-party app.
   * In dev mode, this tells the dev server to hot-reload.
   * In bundle mode, this rebuilds the native bundle and pushes it.
   * The reload command is also broadcast to all connected SDK devices
   * via the BlackBox command channel.
   * @param mode - "dev" for hot reload, "bundle" for native bundle rebuild
   */
  async reloadApp(
    mode: 'dev' | 'bundle' = 'bundle',
    opts?: { projectName?: string; bundleId?: string; projectPath?: string },
  ): Promise<ReloadAck> {
    // Default path: always rebuild a fresh Hermes bundle.
    //
    // Rationale: the SDK's common caller is a phone user who's not
    // sitting at their Mac — they're doing vibe coding with an AI agent
    // editing files on the Mac remotely, or they installed the app via
    // TestFlight and there's no Metro running at all. A Metro-based
    // reload would either fail (Metro offline) or — worse — serve a
    // stale bundle because Metro can be slow to re-index fresh edits.
    // Bundle mode always produces the correct bytecode from the current
    // filesystem state, taking ~30–60 s, and hits /dev/reload-app which
    // uses BlackBox SSE to push the fresh bundle URL to the device.
    //
    // Callers who know they want Metro HMR pass `mode='dev'` explicitly;
    // we still honour that. Everything else defaults to bundle.
    if (mode === 'dev') {
      const primary = await fetch(`${this.baseUrl}/dev/reload`, {
        method: 'POST',
        headers: this.authHeaders(),
      });
      if (primary.ok) {
        const payload = await primary.json().catch(() => ({} as Record<string, unknown>));
        const nativeChangesDetected = payload.nativeChangesDetected === true;
        return {
          ok: true,
          mode: 'dev',
          acknowledged: true,
          nativeChangesDetected,
          changeClass:
            typeof payload.changeClass === 'string' ? payload.changeClass : undefined,
          message: nativeChangesDetected
            ? 'Reload accepted, but native changes need a rebuild.'
            : 'Hot reload request accepted.',
        };
      }
      // Dev mode failed — fall through to bundle rebuild below rather
      // than surfacing the raw error, so the user never has to know
      // Metro wasn't running.
    }

    // Auto-resolve identity if the caller didn't pass it. Reads from
    // expo-constants when present (host can pin via app.json
    // `expo.name` / `ios.bundleIdentifier` / `android.package`); falls
    // back to react-native's NativeModules.SettingsManager.settings
    // (iOS `CFBundleIdentifier`, `CFBundleName`) and Application
    // (Android packageName). On the agent side these resolve to the
    // matching MobileProject in the cached scan, so we don't need
    // `yaver dev start` to have run on the host.
    const identity = resolveAppIdentity(opts);

    const res = await fetch(`${this.baseUrl}/dev/reload-app`, {
      method: 'POST',
      headers: this.authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({
        mode: 'bundle',
        ...identity,
      }),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(friendlyReloadError(res.status, text));
    }
    const payload = await res.json().catch(() => ({} as Record<string, unknown>));
    return {
      ok: true,
      mode: 'bundle',
      acknowledged: true,
      changeClass:
        typeof payload.changeClass === 'string' ? payload.changeClass : undefined,
      nativeChangesDetected: payload.nativeChangesDetected === true,
      message:
        typeof payload.message === 'string' && payload.message.trim()
          ? payload.message
          : 'Reload request acknowledged. Agent is rebuilding the bundle.',
    };
  }

  /**
   * Open a vibing session on the connected agent. Vibing is the Yaver
   * interactive coding-agent flow — `/vibing/execute` creates a task with
   * the project context plus the user's prompt. Returns the task id the
   * caller can poll via `/tasks/{id}` if needed.
   *
   * Requires an owner/CLI/paired token — the `/vibing*` routes do not
   * currently accept SDK-minted tokens. Power users typically drive
   * vibing from Claude Code / the Yaver mobile app; this method is a
   * convenience for the SDK's one-tap bug-report-to-vibing path.
   */
  async vibing(
    prompt: string,
    opts?: { projectName?: string; bundleId?: string; projectPath?: string },
  ): Promise<{ taskId: string }> {
    // Resolve app identity exactly the same way we do for
    // reloadApp — bundle ID from expo-constants or native config.
    // Without this, the agent falls back to "grep the prompt for a
    // word that looks like a project name," which is catastrophically
    // wrong: the prompt 'tapped Vibing' matched 'in' → picked mprint
    // → Claude vibed on the wrong repo. Passing the bundle/name lets
    // the agent go straight to findMobileProjectByName / bundleId.
    const identity = resolveAppIdentity(opts);
    const response = await fetch(`${this.baseUrl}/vibing/execute`, {
      method: 'POST',
      headers: this.authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({
        prompt,
        projectPath: identity.projectPath ?? opts?.projectPath ?? '',
        projectName: identity.projectName,
        bundleId: identity.bundleId,
      }),
    });
    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Vibing failed (${response.status}): ${text}`);
    }
    return response.json();
  }

  async getVibingEligibility(
    opts?: { projectName?: string; bundleId?: string; projectPath?: string },
  ): Promise<{
    canVibe: boolean;
    reason?: string;
    guidance?: string;
    projectName?: string;
    projectPath?: string;
    provider?: string;
    repoFullName?: string;
  }> {
    const identity = resolveAppIdentity(opts);
    const params = new URLSearchParams();
    if (identity.projectName ?? opts?.projectName) {
      params.set('projectName', identity.projectName ?? opts?.projectName ?? '');
    }
    if (identity.bundleId ?? opts?.bundleId) {
      params.set('bundleId', identity.bundleId ?? opts?.bundleId ?? '');
    }
    if (identity.projectPath ?? opts?.projectPath) {
      params.set('projectPath', identity.projectPath ?? opts?.projectPath ?? '');
    }
    const response = await fetch(`${this.baseUrl}/vibing/eligibility?${params.toString()}`, {
      method: 'GET',
      headers: this.authHeaders(),
    });
    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Vibing eligibility failed (${response.status}): ${text}`);
    }
    return response.json();
  }

  /**
   * After uploading a feedback bundle with `uploadFeedback`, call this
   * with the returned report id to create a fix task on the agent. The
   * task includes the feedback's screenshots, errors, and (when available)
   * the BlackBox context for the originating device.
   */
  async triggerFix(feedbackId: string): Promise<{ taskId: string; prompt: string }> {
    const response = await fetch(`${this.baseUrl}/feedback/${encodeURIComponent(feedbackId)}/fix`, {
      method: 'POST',
      headers: this.authHeaders(),
    });
    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Fix trigger failed (${response.status}): ${text}`);
    }
    return response.json();
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
      headers: this.authHeaders({ "Content-Type": "application/json" }),
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
      headers: this.authHeaders(),
    });
  }

  /** Get the current test session status and list of fixes. */
  async getTestSession(): Promise<TestSession> {
    const response = await this.request('GET', '/test-app/status');
    return response.json();
  }

  /**
   * Rotate the SDK token. The old token stays valid for 5 minutes (grace period).
   * After rotation, the client automatically uses the new token.
   * @returns The new token and its expiry time.
   */
  async rotateToken(): Promise<{ token: string; expiresAt: number }> {
    const response = await fetch(`${this.baseUrl}/sdk/token/rotate`, {
      method: 'POST',
      headers: this.authHeaders({ "Content-Type": "application/json" }),
    });

    if (!response.ok) {
      const text = await response.text().catch(() => '');
      throw new Error(`[P2PClient] Token rotation failed (${response.status}): ${text}`);
    }

    const result = await response.json();
    // Auto-update to new token
    this.authToken = result.token;
    return { token: result.token, expiresAt: result.expiresAt };
  }

  // ─── Feature flags (F1) ──────────────────────────────────────────

  /**
   * Evaluate every flag for a userId. Hits /flags/eval which uses
   * SHA256 bucketing against rolloutPercent — stable per user per
   * flag. Results are the dev's source of truth; the SDK caches
   * for 30s in getFlagsCached().
   */
  async flagsEvaluate(userId: string = 'anonymous'): Promise<Record<string, unknown>> {
    const res = await fetch(
      `${this.baseUrl}/flags/eval?userId=${encodeURIComponent(userId)}`,
      { headers: this.authHeaders() },
    );
    if (!res.ok) return {};
    const data = await res.json();
    return data.flags ?? {};
  }

  /** Evaluate a single flag by key — shortcut when you only need one. */
  async flagsEvaluateOne<T = unknown>(
    key: string,
    userId: string = 'anonymous',
  ): Promise<T | undefined> {
    const res = await fetch(
      `${this.baseUrl}/flags/eval?userId=${encodeURIComponent(userId)}&flag=${encodeURIComponent(key)}`,
      { headers: this.authHeaders() },
    );
    if (!res.ok) return undefined;
    const data = await res.json();
    return data.value as T;
  }

  // ─── Releases (R1) ───────────────────────────────────────────────

  /**
   * Ask what bundle this device should run. Returns the latest
   * release in the channel plus a rollout gate. The mobile app
   * uses this on cold start to decide whether to download a new
   * bundle from /releases/bundle.
   */
  async releasesLatest(
    channel: string = 'production',
    deviceId?: string,
  ): Promise<{
    ok: boolean;
    channel: string;
    semver?: string;
    size?: number;
    md5?: string;
    hermesBcVersion?: number;
    bundleUrl?: string;
    rolloutPercent: number;
    inRollout: boolean;
    reason?: string;
  } | null> {
    const params = new URLSearchParams({ channel });
    if (deviceId) params.set('device', deviceId);
    const res = await fetch(`${this.baseUrl}/releases/latest?${params.toString()}`, {
      headers: this.authHeaders(),
    });
    if (!res.ok) return null;
    return res.json();
  }

  /** Download a specific bundle as raw bytes. */
  async releasesDownload(
    channel: string,
    semver: string,
  ): Promise<ArrayBuffer | null> {
    const params = new URLSearchParams({ channel, semver });
    const res = await fetch(`${this.baseUrl}/releases/bundle?${params.toString()}`, {
      headers: this.authHeaders(),
    });
    if (!res.ok) return null;
    return res.arrayBuffer();
  }

  // ─── Analytics ingest (A1 — direct POST path) ───────────────────

  /**
   * Fire-and-forget track event. Most callers should use
   * `BlackBox.track()` which fans through the streaming channel;
   * this method is the fallback for surfaces without a live SSE.
   */
  async analyticsIngest(
    name: string,
    props?: Record<string, string>,
    opts?: { deviceId?: string; route?: string; timestamp?: number },
  ): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseUrl}/analytics/ingest`, {
        method: 'POST',
        headers: this.authHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({
          name,
          props,
          deviceId: opts?.deviceId,
          route: opts?.route,
          timestamp: opts?.timestamp ?? Date.now(),
        }),
      });
      return res.ok;
    } catch {
      return false;
    }
  }

  /** Internal helper for authenticated GET/POST requests. */
  private async request(method: string, path: string): Promise<Response> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: this.authHeaders(),
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
