import { NativeModules } from 'react-native';
import { FeedbackConfig, CapturedError } from './types';
import { YaverDiscovery } from './Discovery';
import { BlackBox } from './BlackBox';
import { P2PClient } from './P2PClient';
import { ShakeDetector } from './ShakeDetector';
import {
  configureAuthEndpoints,
  setStrictNativeAuth,
  getToken,
  getSelectedDeviceId,
  listReachableDevices,
  clearToken,
  clearSelectedDeviceId,
  DEFAULT_CONVEX_SITE_URL,
} from './auth';
import {
  getQuickIconDisabled,
  getQuickIconColorPreset,
  setQuickIconDisabled,
  setQuickIconColorPreset,
  QuickIconColorPreset,
} from './preferences';

// Is this JS runtime the Yaver mobile app's super-host bridge? The
// YaverInfo native module is only registered inside Yaver's container
// (mobile/ios/Yaver/YaverInfo.{swift,m} + Android counterpart); a
// standalone app bundled by its own developer has no such module.
// When the SDK is loaded through Yaver's Hermes-push guest runtime we
// deliberately no-op every public entry point — Yaver owns the shake
// gesture ("Reload / Back to Yaver" overlay), the feedback capture
// flow, and the BlackBox streaming; running a second copy from inside
// the guest just produces duplicate UIs and double P2P sessions.
function isRunningInsideYaverHost(): boolean {
  try {
    return !!(NativeModules as any)?.YaverInfo;
  } catch {
    return false;
  }
}

// Suppresses SDK activation when inside Yaver super-host. Callers of
// YaverFeedback.init / startReport / startBatchRecording / … early-out
// by checking this first so the SDK's side effects (accelerometer,
// BlackBox HTTP, SSE command channel, FeedbackModal mount) never start.
const YAVER_HOST_SUPPRESS = isRunningInsideYaverHost();

let config: FeedbackConfig | null = null;
let enabled = false;
let p2pClient: P2PClient | null = null;
let shakeDetector: ShakeDetector | null = null;

/** Ring buffer of captured errors. */
let errorBuffer: CapturedError[] = [];
let maxErrors = 5;

/** Track whether BlackBox was running before disable (to restart on enable). */
let blackBoxWasStreaming = false;

/**
 * Tracks whether the user has already shaken once in this process.
 * Consumed by QuickActionIcon's `'after-shake'` mode so the icon
 * appears the first time a user discovers shake and stays around
 * thereafter.
 */
let firstShakeFired = false;

/**
 * Flag evaluation cache — 30s TTL per `userId|key`. Prevents a
 * tight render loop from hammering /flags/eval when the dev calls
 * `YaverFeedback.getFlag()` every frame.
 */
const flagCache: Map<string, { value: unknown; at: number }> = new Map();

/**
 * Main entry point for the Yaver Feedback SDK.
 * Call `YaverFeedback.init()` once at app startup.
 */
export class YaverFeedback {
  /**
   * Initialize the feedback SDK with the given configuration.
   * Typically called in your app's root component or entry file.
   *
   * If no `agentUrl` is provided, the SDK will attempt auto-discovery
   * via `YaverDiscovery` on the first `startReport()` call.
   */
  static init(cfg: FeedbackConfig): void {
    if (YAVER_HOST_SUPPRESS) {
      // Running inside Yaver's super-host — yield to Yaver's native UX.
      enabled = false;
      return;
    }
    config = {
      trigger: 'shake',
      maxRecordingDuration: 120,
      autoLogin: true,
      ...cfg,
    };
    firstShakeFired = false;

    // Route the in-SDK login screen to prod yaver.io by default; callers may
    // override for staging via authConvexSiteUrl / authWebBaseUrl.
    configureAuthEndpoints({
      convexSiteUrl: cfg.authConvexSiteUrl,
      webBaseUrl: cfg.authWebBaseUrl,
    });
    // Compile-time lockdown: refuse any browser-hop / device-code fallback
    // and force ASWebAuthenticationSession in ephemeral mode for OAuth.
    setStrictNativeAuth(cfg.strictNativeAuth === true);
    // If no explicit convexUrl was set but we have an auth site URL, use it
    // so Discovery.discoverFromConvex() has somewhere to look up the user's
    // machines (works for both LAN-direct and off-LAN relay paths).
    if (!config.convexUrl) {
      config.convexUrl = cfg.authConvexSiteUrl ?? DEFAULT_CONVEX_SITE_URL;
    }

    // Default: enabled only in dev mode
    if (cfg.enabled !== undefined) {
      enabled = cfg.enabled;
    } else {
      enabled = typeof __DEV__ !== 'undefined' ? __DEV__ : false;
    }

    // Hydrate cached auth token + preferred device from AsyncStorage so the
    // SDK reconnects silently on subsequent launches. If autoLogin is false
    // the caller is responsible for providing authToken themselves.
    if (config.autoLogin !== false && enabled) {
      void YaverFeedback.hydrateSession();
    }

    // Create P2P client if we have a URL
    if (config.agentUrl) {
      p2pClient = new P2PClient(config.agentUrl, config.authToken ?? '');
    } else {
      p2pClient = null;
      // Auto-discover agent in the background when convexUrl or LAN is available
      if (enabled && (config.authToken || config.preferredDeviceId)) {
        YaverFeedback.discoverAgent();
      }
    }

    // Set up error capture buffer size
    maxErrors = cfg.maxCapturedErrors ?? 5;
    errorBuffer = [];

    // Wire up shake detection when trigger is 'shake'
    if (shakeDetector) {
      shakeDetector.stop();
      shakeDetector = null;
    }
    if (enabled && config.trigger === 'shake') {
      shakeDetector = new ShakeDetector();
      shakeDetector.start(() => {
        YaverFeedback.notifyShake();
        if (config?.reportingOnly) {
          YaverFeedback.sendAutoReport();
        } else {
          YaverFeedback.startReport();
        }
      });
    }

    // Wire up BlackBox command handlers for reload + status signals from
    // the agent. Opens an SSE channel the agent uses to:
    //   - broadcast reload_bundle so the phone picks up a fresh Hermes
    //     bundle after a vibe-coding edit;
    //   - stream build/compile progress ("Compiling bundle…", "Pushing
    //     assets…", "Done") so the SDK can surface it like a normal
    //     "Working…" spinner instead of a silent 60-second freeze.
    if (enabled) {
      BlackBox.onCommand((cmd) => {
        if (cmd.command === 'reload') {
          if (cfg.onReload) {
            cfg.onReload();
          } else {
            YaverFeedback.defaultReload();
          }
        } else if (cmd.command === 'reload_bundle' && cmd.data) {
          const bundleUrl = cmd.data.bundleUrl as string;
          const assetsUrl = cmd.data.assetsUrl as string | undefined;
          if (cfg.onReloadBundle) {
            cfg.onReloadBundle(bundleUrl, assetsUrl);
          } else {
            YaverFeedback.defaultReloadBundle(bundleUrl, assetsUrl);
          }
        } else if (cmd.command === 'status') {
          // Pipe agent progress pings to the UI. The FeedbackModal
          // subscribes to this event and renders the message + a
          // progress bar while a reload / build is in flight.
          const message =
            typeof cmd.data?.message === 'string'
              ? (cmd.data.message as string)
              : '';
          const phase =
            typeof cmd.data?.phase === 'string'
              ? (cmd.data.phase as string)
              : '';
          const progress =
            typeof cmd.data?.progress === 'number'
              ? Math.max(0, Math.min(1, cmd.data.progress as number))
              : undefined;
          const { DeviceEventEmitter } = require('react-native');
          DeviceEventEmitter.emit('yaverFeedback:status', {
            message,
            phase,
            progress,
            at: Date.now(),
          });
        }
      });
      // NOTE: BlackBox.start() is intentionally NOT auto-called here.
      // An earlier version (0.7.6) did auto-start it, and when the
      // agent was in bootstrap / needs-auth mode — which can happen
      // any time after `yaver serve` restarts before the user pairs —
      // the SSE channel retried with exponential backoff on 401s,
      // producing a tight loop of string concatenation + JSON parse
      // that tripped a Hermes rope-string SIGSEGV on iOS 18.3.1
      // during any other JS-thread regex work (e.g. react-native-
      // view-shot's internal string handling during Screenshot &
      // Fix). Host apps call BlackBox.start() explicitly once they
      // know the agent URL + token are valid (SFMG does this inside
      // its YaverFeedbackWidget after auth).
    }

    // NOTE: We intentionally do NOT hook ErrorUtils.setGlobalHandler().
    // Sentry, Crashlytics, Bugsnag, and other tools all compete for that
    // single slot. Hijacking it would break whichever tool the developer
    // already has installed, depending on init order.
    //
    // Instead, developers use:
    //   - YaverFeedback.attachError(err) in catch blocks
    //   - YaverFeedback.wrapErrorHandler(existingHandler) to create a
    //     pass-through wrapper they insert into their own error chain
  }

  /**
   * Run agent discovery in the background.
   * Called automatically from init() when no agentUrl is provided.
   * Sets config.agentUrl and creates P2PClient on success.
   */
  static async discoverAgent(): Promise<void> {
    if (!config || !enabled) return;
    if (config.agentUrl) return; // already have a URL
    if (!config.authToken) return; // need auth before discovery can succeed

    try {
      const result = await YaverDiscovery.discover({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (result && config) {
        config.agentUrl = result.url;
        p2pClient = new P2PClient(result.url, config.authToken ?? '');
      }
    } catch {
      // Discovery failed — FloatingButton will show disconnected, user can retry
    }
  }

  /**
   * Force a fresh Convex lookup for the agent URL — ignoring any
   * cached URL. Callers use this after a P2P request fails
   * (connection refused / timeout) because the most common cause is
   * the Mac's LAN IP rotating. Convex has the fresh one, so we
   * re-query and probe `[quicHost, ...localIps]` in parallel.
   *
   * Returns true when a new URL was adopted.
   */
  static async reconnect(): Promise<boolean> {
    if (!config || !enabled) return false;
    if (!config.authToken || !config.convexUrl) return false;
    try {
      const result = await YaverDiscovery.refreshFromConvex({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (!result) return false;
      config.agentUrl = result.url;
      p2pClient = new P2PClient(result.url, config.authToken ?? '');
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Pull a cached session token + selected device from AsyncStorage (populated
   * by the in-SDK login + machine-picker screens). When present the SDK can
   * reconnect silently on launch without re-prompting the user. Safe to call
   * multiple times — it only overrides values the caller did not already set.
   */
  static async hydrateSession(): Promise<void> {
    if (!config) return;
    try {
      if (!config.authToken) {
        const cached = await getToken();
        if (cached) {
          config.authToken = cached;
        }
      }
      if (!config.preferredDeviceId) {
        const cachedDevice = await getSelectedDeviceId();
        if (cachedDevice) {
          config.preferredDeviceId = cachedDevice;
        }
      }
      if (config.authToken && !config.agentUrl) {
        await YaverFeedback.discoverAgent();
      }
    } catch {
      // hydration best-effort
    }
  }

  /**
   * Update the signed-in session token (e.g. after the in-SDK login screen
   * succeeds). Rebuilds the P2P client and kicks off agent discovery.
   */
  static async setAuthToken(token: string): Promise<void> {
    if (!config) return;
    config.authToken = token;
    if (config.agentUrl) {
      p2pClient = new P2PClient(config.agentUrl, token);
    } else {
      await YaverFeedback.discoverAgent();
    }
  }

  /** Returns true once the SDK has a session token it can use. */
  static isAuthed(): boolean {
    return Boolean(config?.authToken);
  }

  /**
   * Request the embedded FeedbackModal to show the login screen. Works by
   * emitting an event the modal listens for — avoids forcing the host app
   * to mount a second navigator.
   */
  static showLogin(): void {
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startLogin');
  }

  /**
   * Request the embedded FeedbackModal to show the machine picker. Requires
   * an active session; no-ops otherwise.
   */
  static showMachinePicker(): void {
    if (!YaverFeedback.isAuthed()) return;
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startMachinePicker');
  }

  /**
   * Update the selected remote device. Resets the cached agent URL so the
   * next `startReport()` (or FloatingButton press) rediscovers against the
   * newly-selected machine.
   */
  static async setPreferredDevice(deviceId: string): Promise<void> {
    if (!config) return;
    config.preferredDeviceId = deviceId;
    config.agentUrl = undefined;
    p2pClient = null;
    await YaverFeedback.discoverAgent();
  }

  /** Resolve the currently selected remote machine from the authenticated device list. */
  static async getSelectedRemoteDevice() {
    if (!config?.authToken || !config.preferredDeviceId) return null;
    const preferredDeviceId = config.preferredDeviceId;
    const devices = await listReachableDevices(config.authToken);
    const all = [...devices.owned, ...devices.shared];
    return all.find((device) => device.deviceId === preferredDeviceId) ?? null;
  }

  /**
   * Sign out: clear cached token + device, tear down the P2P client. The
   * SDK stays enabled; the next feedback trigger will re-prompt for login.
   */
  static async signOut(): Promise<void> {
    await clearToken();
    await clearSelectedDeviceId();
    if (config) {
      config.authToken = undefined;
      config.preferredDeviceId = undefined;
      config.agentUrl = undefined;
    }
    p2pClient = null;
  }

  /**
   * Manually trigger the feedback collection flow.
   * Opens the feedback modal if the SDK is initialized and enabled.
   *
   * If no agentUrl was configured, runs auto-discovery first.
   */
  static async startReport(): Promise<void> {
    if (!config) {
      console.warn('[YaverFeedback] SDK not initialized. Call YaverFeedback.init() first.');
      return;
    }
    if (!enabled) {
      return;
    }

    // If the caller has autoLogin enabled and we have no session yet, show
    // the in-SDK login flow instead of a failing discovery + warning spam.
    if (!config.authToken) {
      if (config.autoLogin !== false) {
        await YaverFeedback.hydrateSession();
      }
      if (!config.authToken) {
        YaverFeedback.showLogin();
        return;
      }
    }

    // Auto-discover if no agent URL was provided
    if (!config.agentUrl) {
      try {
        const result = await YaverDiscovery.discover({
          convexUrl: config.convexUrl,
          authToken: config.authToken,
          preferredDeviceId: config.preferredDeviceId,
        });
        if (result) {
          config.agentUrl = result.url;
          p2pClient = new P2PClient(result.url, config.authToken ?? '');
        } else if (config.autoLogin !== false && !config.preferredDeviceId) {
          // No agent discovered and no device picked yet — prompt the user
          // to pick one of their machines (handles the non-LAN case where
          // relay discovery requires knowing which deviceId to target).
          YaverFeedback.showMachinePicker();
          return;
        } else {
          console.warn('[YaverFeedback] No agent found. Check that `yaver serve` is running on the selected machine.');
        }
      } catch (err) {
        console.warn('[YaverFeedback] Auto-discovery failed:', err);
      }
    }

    // Emit event that the FeedbackModal listens for
    const { DeviceEventEmitter } = require('react-native');
    DeviceEventEmitter.emit('yaverFeedback:startReport');
  }

  /** Returns true if the SDK has been initialized. */
  static isInitialized(): boolean {
    return config !== null;
  }

  /**
   * Enable or disable the entire feedback SDK at runtime.
   *
   * **Disable (false):**
   * - Stops BlackBox streaming (flushes remaining events first)
   * - Restores console.log/warn/error if wrapped
   * - Clears error buffer
   * - All methods become no-ops (attachError, wrapErrorHandler still safe to call but do nothing)
   * - P2P client is kept alive (no reconnection cost on re-enable)
   *
   * **Enable (true):**
   * - Restarts BlackBox streaming if it was running before disable
   * - Error buffer starts collecting again
   * - All methods become active
   */
  static setEnabled(value: boolean): void {
    if (enabled === value) return; // No-op if already in desired state

    if (!value) {
      // === DISABLE ===
      blackBoxWasStreaming = BlackBox.isStreaming;
      if (BlackBox.isStreaming) {
        BlackBox.stop(); // flush + stop timer + unwrap console
      }
      BlackBox.unwrapConsole(); // ensure console is restored even if BlackBox wasn't started
      errorBuffer = [];
      if (shakeDetector) {
        shakeDetector.stop();
        shakeDetector = null;
      }
    } else {
      // === ENABLE ===
      if (blackBoxWasStreaming) {
        BlackBox.start(); // restart with previous config
      }
      // Restart shake detector if trigger is 'shake'
      if (config?.trigger === 'shake' && !shakeDetector) {
        shakeDetector = new ShakeDetector();
        shakeDetector.start(() => {
          YaverFeedback.notifyShake();
          if (config?.reportingOnly) {
            YaverFeedback.sendAutoReport();
          } else {
            YaverFeedback.startReport();
          }
        });
      }
    }

    enabled = value;
  }

  /** Returns whether the SDK is currently enabled. */
  static isEnabled(): boolean {
    return enabled;
  }

  /** Returns the current config, or null if not initialized. */
  static getConfig(): FeedbackConfig | null {
    return config;
  }

  /**
   * Manually attach an error with optional metadata.
   * Use this in catch blocks to give the agent extra context.
   */
  static attachError(error: Error, metadata?: Record<string, unknown>): void {
    if (!enabled) return; // No-op when disabled
    const captured: CapturedError = {
      message: error.message,
      stack: (error.stack ?? '').split('\n').filter((l: string) => l.trim()),
      isFatal: false,
      timestamp: Date.now(),
      metadata,
    };
    errorBuffer.push(captured);
    if (errorBuffer.length > maxErrors) {
      errorBuffer.shift();
    }
  }

  /**
   * Returns the current captured errors buffer.
   * Called internally when building a FeedbackBundle.
   */
  static getCapturedErrors(): CapturedError[] {
    return [...errorBuffer];
  }

  /** Clears the captured errors buffer. */
  static clearCapturedErrors(): void {
    errorBuffer = [];
  }

  /**
   * Returns a pass-through error handler that records the error in Yaver's
   * buffer and then calls `next`. Use this to insert Yaver into your
   * existing error handler chain without replacing anything.
   *
   * @example
   * // Works alongside Sentry, Crashlytics, or any other tool:
   * const originalHandler = ErrorUtils.getGlobalHandler();
   * ErrorUtils.setGlobalHandler(
   *   YaverFeedback.wrapErrorHandler(originalHandler)
   * );
   * // Sentry can still be initialized after this — it will wrap our
   * // wrapper, and the chain stays intact.
   */
  static wrapErrorHandler(
    next?: ((error: Error, isFatal?: boolean) => void) | null,
  ): (error: Error, isFatal?: boolean) => void {
    return (error: Error, isFatal?: boolean) => {
      YaverFeedback.attachError(error);
      if (errorBuffer.length > 0) {
        errorBuffer[errorBuffer.length - 1].isFatal = isFatal ?? false;
      }
      next?.(error, isFatal);
    };
  }

  /**
   * Returns the P2P client instance.
   * Available after init if agentUrl is set, or after first successful discovery.
   */
  static getP2PClient(): P2PClient | null {
    return p2pClient;
  }

  // ─── One-stop SaaS replacement methods ─────────────────────────
  //
  // These are the three solo-dev SaaS-replacement entry points
  // wired into YaverFeedback so there's exactly one import path
  // for the dev's app code: track / getFlag / checkUpdate.

  /**
   * Record a business event. Routes through BlackBox so the agent
   * persists it to the analytics ledger (no dashboards — export
   * via CSV or webhook into PostHog).
   *
   * @example
   * ```ts
   * YaverFeedback.track('purchase_completed', { amount: '9.99' });
   * ```
   */
  static track(name: string, props?: Record<string, unknown>, route?: string): void {
    if (!enabled) return;
    BlackBox.track(name, props, route);
  }

  /**
   * Evaluate a single feature flag for a user. Results are cached
   * for 30 seconds inside YaverFeedback so a tight loop evaluating
   * the same key doesn't hammer the agent.
   *
   * @param key — flag key (must exist on the agent)
   * @param defaultValue — returned if the flag is missing / offline
   * @param userId — stable user identifier for rollout bucketing
   */
  static async getFlag<T = boolean | string>(
    key: string,
    defaultValue: T,
    userId: string = 'anonymous',
  ): Promise<T> {
    if (!enabled || !p2pClient) return defaultValue;
    const cacheKey = `${userId}|${key}`;
    const now = Date.now();
    const cached = flagCache.get(cacheKey);
    if (cached && now - cached.at < 30_000) {
      return (cached.value as T) ?? defaultValue;
    }
    try {
      const val = await p2pClient.flagsEvaluateOne<T>(key, userId);
      flagCache.set(cacheKey, { value: val ?? defaultValue, at: now });
      return (val as T) ?? defaultValue;
    } catch {
      return defaultValue;
    }
  }

  /**
   * Bulk evaluate every flag for a user. Cached on the same 30s
   * window as getFlag — use this when boot needs a handful of
   * flags in one roundtrip.
   */
  static async getFlags(
    userId: string = 'anonymous',
  ): Promise<Record<string, unknown>> {
    if (!enabled || !p2pClient) return {};
    const cacheKey = `all|${userId}`;
    const now = Date.now();
    const cached = flagCache.get(cacheKey);
    if (cached && now - cached.at < 30_000) {
      return (cached.value as Record<string, unknown>) ?? {};
    }
    try {
      const flags = await p2pClient.flagsEvaluate(userId);
      flagCache.set(cacheKey, { value: flags, at: now });
      return flags;
    } catch {
      return {};
    }
  }

  /**
   * Ask what bundle this device should run. Returns the latest
   * release manifest in the configured channel plus a rollout
   * gate. The dev can then compare against what's currently
   * running and prompt the user to reload.
   *
   * On-disk bundle swap is platform-specific — see
   * `YaverFeedback.onUpdateAvailable` if you want a hook.
   */
  static async checkUpdate(
    channel: string = 'production',
    deviceId?: string,
  ): Promise<Awaited<ReturnType<P2PClient['releasesLatest']>>> {
    if (!enabled || !p2pClient) return null;
    return p2pClient.releasesLatest(channel, deviceId);
  }

  /** Clear the in-memory flag cache. Useful for tests or after sign-out. */
  static clearFlagCache(): void {
    flagCache.clear();
  }

  /**
   * Reporting-only mode: auto-capture screenshot + errors and send
   * to the agent's /feedback endpoint. No modal UI — just shake and go.
   *
   * This is triggered by shake when `reportingOnly: true` is set.
   * The agent receives the report via the same P2P channel and logs it.
   */
  static async sendAutoReport(): Promise<void> {
    if (!config || !enabled) return;

    // Resolve agent URL if needed
    if (!config.agentUrl) {
      try {
        const result = await YaverDiscovery.discover({
          convexUrl: config.convexUrl,
          authToken: config.authToken,
          preferredDeviceId: config.preferredDeviceId,
        });
        if (result) {
          config.agentUrl = result.url;
          p2pClient = new P2PClient(result.url, config.authToken ?? '');
        }
      } catch {}
    }

    if (!config.agentUrl) {
      console.warn('[YaverFeedback] No agent URL — cannot send auto report.');
      return;
    }

    try {
      const { Platform, Dimensions } = require('react-native');
      const { captureScreenshot } = require('./capture');
      const { uploadFeedback } = require('./upload');
      const { width, height } = Dimensions.get('window');

      // Auto-capture screenshot
      let screenshotPath: string | undefined;
      try {
        screenshotPath = await captureScreenshot();
      } catch {
        // Screenshot capture may fail (e.g. no view ref) — continue without it
      }

      const bundle = {
        metadata: {
          timestamp: new Date().toISOString(),
          device: {
            platform: Platform.OS,
            osVersion: String(Platform.Version),
            model: Platform.OS === 'ios' ? 'iOS Device' : 'Android Device',
            screenWidth: width,
            screenHeight: height,
          },
          app: {},
          userNote: '[Auto-report via shake]',
        },
        screenshots: screenshotPath ? [screenshotPath] : [],
        errors: errorBuffer.length > 0 ? [...errorBuffer] : undefined,
      };

      await uploadFeedback(config.agentUrl, config.authToken ?? '', bundle);
      console.log('[YaverFeedback] Auto-report sent');
    } catch (err) {
      console.warn('[YaverFeedback] Auto-report failed:', err);
    }
  }

  /**
   * Default reload handler. Tries three strategies in order:
   *
   * 1. **YaverBundleLoader** — running inside Yaver's native container.
   *    Pulls fresh Hermes bundle from agent and reloads the RN bridge.
   *
   * 2. **YaverHotReload** — standalone app with feedback SDK's native module
   *    (added via Expo config plugin). Downloads Hermes bundle from agent,
   *    saves to Documents, and reloads the RN bridge.
   *
   * 3. **DevSettings.reload()** — standalone dev build connected to Metro.
   */
  private static defaultReload(): void {
    if (!config?.agentUrl) return;
    const bundleUrl = `${config.agentUrl}/dev/native-bundle`;
    const headers = { Authorization: `Bearer ${config.authToken ?? ''}` };
    YaverFeedback.loadBundleAndReload(bundleUrl, headers);
  }

  /**
   * Default reload_bundle handler. Receives a compiled Hermes bundle URL
   * from the agent and loads it via the best available native mechanism.
   */
  private static defaultReloadBundle(bundleUrl: string, _assetsUrl?: string): void {
    if (!config?.agentUrl) return;

    const fullUrl = bundleUrl.startsWith('http')
      ? bundleUrl
      : `${config.agentUrl}${bundleUrl}`;
    const headers = { Authorization: `Bearer ${config.authToken ?? ''}` };
    YaverFeedback.loadBundleAndReload(fullUrl, headers);
  }

  /**
   * Core bundle reload logic. Tries native loaders in order:
   *
   * 1. YaverBundleLoader (Yaver container — full validation + bridge reload)
   * 2. YaverHotReload (SDK's own native module — download + bridge reload)
   * 3. DevSettings.reload() (Metro dev server fallback)
   */
  private static loadBundleAndReload(
    bundleUrl: string,
    headers: Record<string, string>,
  ): void {
    const { NativeModules } = require('react-native');

    // Strategy 1: YaverBundleLoader (running inside Yaver container)
    if (NativeModules.YaverBundleLoader) {
      NativeModules.YaverBundleLoader.loadBundle(bundleUrl, 'main', headers)
        .catch((err: Error) => {
          console.warn('[YaverFeedback] YaverBundleLoader reload failed:', err);
        });
      return;
    }

    // Strategy 2: YaverHotReload (SDK's native module, added by Expo config plugin)
    if (NativeModules.YaverHotReload) {
      NativeModules.YaverHotReload.loadBundle(bundleUrl, headers)
        .catch((err: Error) => {
          console.warn('[YaverFeedback] YaverHotReload reload failed:', err);
        });
      return;
    }

    // Strategy 3: DevSettings.reload() for Metro dev builds
    console.warn(
      '[YaverFeedback] No native bundle loader available. ' +
      'Add "yaver-feedback-react-native" to your app.json plugins to enable hot reload.',
    );
    try {
      const { DevSettings } = require('react-native');
      if (typeof DevSettings?.reload === 'function') {
        DevSettings.reload();
      }
    } catch {
      // Not in dev mode
    }
  }

  /**
   * Internal: fired from every shake path (dev-menu + accelerometer)
   * before the feedback modal opens. Emits `yaverFeedback:firstShake`
   * exactly once per process so QuickActionIcon's `'after-shake'` mode
   * can surface itself on first shake and stay visible for the rest of
   * the session.
   */
  static notifyShake(): void {
    if (firstShakeFired) return;
    firstShakeFired = true;
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:firstShake');
    } catch {
      // emitter unavailable (e.g. jsdom unit test) — safe to ignore
    }
  }

  /**
   * Show / hide the QuickActionIcon programmatically and persist the
   * choice across launches. Host apps can call this from a settings
   * screen so the user has a second way to re-enable the icon after
   * hiding it via the icon's own long-press menu — shake is always the
   * third back-door because it never depends on a visible control.
   */
  static async setQuickIconVisible(visible: boolean): Promise<void> {
    await setQuickIconDisabled(!visible);
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit(
        visible ? 'yaverFeedback:quickIconShow' : 'yaverFeedback:quickIconHide',
      );
    } catch {
      // emitter unavailable — preference is still persisted
    }
  }

  /**
   * Returns `true` when the user has chosen to hide the QuickActionIcon
   * (via its long-press menu or `setQuickIconVisible(false)`).
   * FeedbackModal uses this to surface a one-tap "Show quick icon"
   * control so the user can bring the icon back without having to know
   * about the programmatic API.
   */
  static async isQuickIconHidden(): Promise<boolean> {
    return getQuickIconDisabled();
  }

  static async setQuickIconColorPreset(
    preset: QuickIconColorPreset | null,
  ): Promise<void> {
    await setQuickIconColorPreset(preset);
    try {
      const { DeviceEventEmitter } = require('react-native');
      DeviceEventEmitter.emit('yaverFeedback:quickIconColorChange', { preset });
    } catch {
      // emitter unavailable — preference is still persisted
    }
  }

  static async getQuickIconColorPreset(): Promise<QuickIconColorPreset | null> {
    return getQuickIconColorPreset();
  }

  /** Clear the persisted "user hid the icon" flag. */
  static async resetQuickIconPreference(): Promise<void> {
    await YaverFeedback.setQuickIconVisible(true);
  }

  /** Tear down the SDK (stop shake detector, clear state). */
  static destroy(): void {
    if (shakeDetector) {
      shakeDetector.stop();
      shakeDetector = null;
    }
    firstShakeFired = false;
    enabled = false;
    config = null;
    p2pClient = null;
    errorBuffer = [];
  }
}
