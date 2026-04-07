import { FeedbackConfig, CapturedError } from './types';
import { YaverDiscovery } from './Discovery';
import { BlackBox } from './BlackBox';
import { P2PClient } from './P2PClient';
import { ShakeDetector } from './ShakeDetector';

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
    config = {
      trigger: 'shake',
      maxRecordingDuration: 120,
      feedbackMode: 'batch',
      agentCommentaryLevel: 0,
      ...cfg,
    };

    // Default: enabled only in dev mode
    if (cfg.enabled !== undefined) {
      enabled = cfg.enabled;
    } else {
      enabled = typeof __DEV__ !== 'undefined' ? __DEV__ : false;
    }

    // Create P2P client if we have a URL
    if (config.agentUrl) {
      p2pClient = new P2PClient(config.agentUrl, config.authToken);
    } else {
      p2pClient = null;
      // Auto-discover agent in the background when convexUrl or LAN is available
      if (enabled) {
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
        if (config?.reportingOnly) {
          YaverFeedback.sendAutoReport();
        } else {
          YaverFeedback.startReport();
        }
      });
    }

    // Wire up BlackBox command handlers for reload signals from the agent.
    // This enables the vibe coder to trigger reload from the Yaver mobile app
    // and have the third-party app (with this SDK) automatically reload.
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
        }
      });
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

    try {
      const result = await YaverDiscovery.discover({
        convexUrl: config.convexUrl,
        authToken: config.authToken,
        preferredDeviceId: config.preferredDeviceId,
      });
      if (result && config) {
        config.agentUrl = result.url;
        p2pClient = new P2PClient(result.url, config.authToken);
      }
    } catch {
      // Discovery failed — FloatingButton will show disconnected, user can retry
    }
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
          p2pClient = new P2PClient(result.url, config.authToken);
        } else {
          console.warn('[YaverFeedback] No agent found. Set agentUrl, convexUrl, or ensure agent is running on the network.');
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

  /** Returns the current feedback mode. */
  static getFeedbackMode(): 'live' | 'narrated' | 'batch' {
    return config?.feedbackMode ?? 'batch';
  }

  /** Returns the agent commentary level (0-10). */
  static getCommentaryLevel(): number {
    return config?.agentCommentaryLevel ?? 0;
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
          p2pClient = new P2PClient(result.url, config.authToken);
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

      await uploadFeedback(config.agentUrl, config.authToken, bundle);
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
    const headers = { Authorization: `Bearer ${config.authToken}` };
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
    const headers = { Authorization: `Bearer ${config.authToken}` };
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

  /** Tear down the SDK (stop shake detector, clear state). */
  static destroy(): void {
    if (shakeDetector) {
      shakeDetector.stop();
      shakeDetector = null;
    }
    enabled = false;
    config = null;
    p2pClient = null;
    errorBuffer = [];
  }
}
