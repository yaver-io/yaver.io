import { Platform } from 'react-native';
import { CapturedError } from './types';
import { YaverFeedback } from './YaverFeedback';

/**
 * Black box event types streamed from the device to the agent.
 * These mirror the Go BlackBoxEvent struct on the agent side.
 */
export interface BlackBoxEvent {
  type: 'log' | 'error' | 'navigation' | 'lifecycle' | 'network' | 'state' | 'render';
  level?: 'info' | 'warn' | 'error';
  message: string;
  timestamp: number;
  stack?: string[];
  isFatal?: boolean;
  metadata?: Record<string, unknown>;
  source?: string;
  duration?: number;
  route?: string;
  prevRoute?: string;
}

/** Configuration for the black box stream. */
export interface BlackBoxConfig {
  /** Device identifier (defaults to a generated UUID). */
  deviceId?: string;
  /** Application name for the agent to display. */
  appName?: string;
  /** Flush interval in ms — how often buffered events are sent. Default: 2000. */
  flushInterval?: number;
  /** Max events to buffer before flushing. Default: 50. */
  maxBufferSize?: number;
}

/**
 * Flight-recorder-style streaming from the device to the Yaver agent.
 *
 * Captures logs, errors, navigation, lifecycle events, network requests,
 * and state changes — then streams them continuously to the agent's
 * `/blackbox/events` endpoint.
 *
 * The agent uses this context when the developer asks for a fix — it
 * already knows what the app was doing.
 *
 * **Does not hijack any global handlers.** All capture is explicit:
 * - Call `BlackBox.log()` / `.warn()` / `.error()` for console-style logs
 * - Call `BlackBox.navigation()` for screen changes
 * - Call `BlackBox.networkRequest()` for HTTP activity
 * - Use `BlackBox.wrapConsole()` to intercept console.log/warn/error
 *   (only if you explicitly opt in — no auto-hooking)
 */
export class BlackBox {
  private static baseUrl: string | null = null;
  private static authToken: string | null = null;
  private static deviceId: string = '';
  private static appName: string = '';
  private static buffer: BlackBoxEvent[] = [];
  private static flushTimer: ReturnType<typeof setInterval> | null = null;
  private static flushInterval = 2000;
  private static maxBufferSize = 50;
  private static started = false;
  private static originalConsole: {
    log: typeof console.log;
    warn: typeof console.warn;
    error: typeof console.error;
  } | null = null;

  /**
   * Start the black box stream. Call after `YaverFeedback.init()`.
   *
   * The stream sends buffered events to the agent every `flushInterval` ms,
   * or immediately when the buffer reaches `maxBufferSize`.
   */
  static start(config?: BlackBoxConfig): void {
    const feedbackConfig = YaverFeedback.getConfig();
    if (!feedbackConfig?.agentUrl) {
      console.warn('[BlackBox] No agent URL. Call YaverFeedback.init() first or set agentUrl.');
      return;
    }

    BlackBox.baseUrl = feedbackConfig.agentUrl.replace(/\/$/, '');
    BlackBox.authToken = feedbackConfig.authToken;
    BlackBox.deviceId = config?.deviceId ?? BlackBox.generateDeviceId();
    BlackBox.appName = config?.appName ?? '';
    BlackBox.flushInterval = config?.flushInterval ?? 2000;
    BlackBox.maxBufferSize = config?.maxBufferSize ?? 50;
    BlackBox.buffer = [];
    BlackBox.started = true;

    // Start periodic flush
    if (BlackBox.flushTimer) clearInterval(BlackBox.flushTimer);
    BlackBox.flushTimer = setInterval(() => BlackBox.flush(), BlackBox.flushInterval);

    // Log the session start
    BlackBox.push({
      type: 'lifecycle',
      message: 'Black box streaming started',
      timestamp: Date.now(),
    });
  }

  /** Stop the black box stream and flush remaining events. */
  static stop(): void {
    if (!BlackBox.started) return;
    BlackBox.push({
      type: 'lifecycle',
      message: 'Black box streaming stopped',
      timestamp: Date.now(),
    });
    BlackBox.flush();
    if (BlackBox.flushTimer) {
      clearInterval(BlackBox.flushTimer);
      BlackBox.flushTimer = null;
    }
    BlackBox.started = false;
  }

  /** Whether the black box is currently streaming. */
  static get isStreaming(): boolean {
    return BlackBox.started;
  }

  // ─── Logging ─────────────────────────────────────────────────────

  static log(message: string, source?: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({ type: 'log', level: 'info', message, timestamp: Date.now(), source, metadata });
  }

  static warn(message: string, source?: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({ type: 'log', level: 'warn', message, timestamp: Date.now(), source, metadata });
  }

  static error(message: string, source?: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({ type: 'log', level: 'error', message, timestamp: Date.now(), source, metadata });
  }

  // ─── Errors ──────────────────────────────────────────────────────

  /** Record a caught error with stack trace. Also adds to the feedback error buffer. */
  static captureError(err: Error, isFatal = false, metadata?: Record<string, unknown>): void {
    const stack = (err.stack ?? '').split('\n').filter((l: string) => l.trim());
    BlackBox.push({
      type: 'error',
      message: err.message,
      timestamp: Date.now(),
      stack,
      isFatal,
      metadata,
    });
    // Also feed into the feedback SDK error buffer
    YaverFeedback.attachError(err, metadata);
  }

  // ─── Navigation ──────────────────────────────────────────────────

  /** Record a screen/route navigation event. */
  static navigation(route: string, prevRoute?: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({
      type: 'navigation',
      message: `Navigate: ${prevRoute ? prevRoute + ' -> ' : ''}${route}`,
      timestamp: Date.now(),
      route,
      prevRoute,
      metadata,
    });
  }

  // ─── Lifecycle ───────────────────────────────────────────────────

  /** Record an app lifecycle event (mount, unmount, background, foreground). */
  static lifecycle(event: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({ type: 'lifecycle', message: event, timestamp: Date.now(), metadata });
  }

  // ─── Network ─────────────────────────────────────────────────────

  /** Record a network request/response. */
  static networkRequest(
    method: string,
    url: string,
    status?: number,
    durationMs?: number,
    metadata?: Record<string, unknown>,
  ): void {
    const msg = status != null
      ? `${method} ${url} → ${status}`
      : `${method} ${url}`;
    BlackBox.push({
      type: 'network',
      message: msg,
      timestamp: Date.now(),
      duration: durationMs,
      metadata,
    });
  }

  // ─── State ───────────────────────────────────────────────────────

  /** Record a state change event (Redux action, context update, etc.). */
  static stateChange(description: string, metadata?: Record<string, unknown>): void {
    BlackBox.push({ type: 'state', message: description, timestamp: Date.now(), metadata });
  }

  // ─── Render ──────────────────────────────────────────────────────

  /** Record a render/re-render event with optional duration. */
  static render(component: string, durationMs?: number, metadata?: Record<string, unknown>): void {
    BlackBox.push({
      type: 'render',
      message: component,
      timestamp: Date.now(),
      duration: durationMs,
      metadata,
    });
  }

  // ─── Console wrapping (opt-in) ───────────────────────────────────

  /**
   * Wrap `console.log`, `console.warn`, and `console.error` to also
   * stream them to the black box. **Call this explicitly** — the SDK
   * never auto-hooks console.
   *
   * Call `BlackBox.unwrapConsole()` to restore originals.
   */
  static wrapConsole(): void {
    if (BlackBox.originalConsole) return; // Already wrapped
    BlackBox.originalConsole = {
      log: console.log,
      warn: console.warn,
      error: console.error,
    };
    console.log = (...args: unknown[]) => {
      BlackBox.originalConsole!.log(...args);
      BlackBox.log(args.map(String).join(' '));
    };
    console.warn = (...args: unknown[]) => {
      BlackBox.originalConsole!.warn(...args);
      BlackBox.warn(args.map(String).join(' '));
    };
    console.error = (...args: unknown[]) => {
      BlackBox.originalConsole!.error(...args);
      BlackBox.error(args.map(String).join(' '));
    };
  }

  /** Restore original console methods. */
  static unwrapConsole(): void {
    if (!BlackBox.originalConsole) return;
    console.log = BlackBox.originalConsole.log;
    console.warn = BlackBox.originalConsole.warn;
    console.error = BlackBox.originalConsole.error;
    BlackBox.originalConsole = null;
  }

  // ─── Error handler wrapper (opt-in) ──────────────────────────────

  /**
   * Returns a pass-through error handler that streams errors to the
   * black box AND calls the next handler. Same pattern as
   * `YaverFeedback.wrapErrorHandler`, but streams in real-time.
   *
   * @example
   * const existing = ErrorUtils.getGlobalHandler();
   * ErrorUtils.setGlobalHandler(BlackBox.wrapErrorHandler(existing));
   */
  static wrapErrorHandler(
    next?: ((error: Error, isFatal?: boolean) => void) | null,
  ): (error: Error, isFatal?: boolean) => void {
    return (error: Error, isFatal?: boolean) => {
      BlackBox.captureError(error, isFatal ?? false);
      next?.(error, isFatal);
    };
  }

  // ─── Internal ────────────────────────────────────────────────────

  private static push(event: BlackBoxEvent): void {
    if (!BlackBox.started) return;
    BlackBox.buffer.push(event);
    if (BlackBox.buffer.length >= BlackBox.maxBufferSize) {
      BlackBox.flush();
    }
  }

  private static async flush(): Promise<void> {
    if (!BlackBox.baseUrl || !BlackBox.authToken || BlackBox.buffer.length === 0) return;

    const events = BlackBox.buffer;
    BlackBox.buffer = [];

    try {
      await fetch(`${BlackBox.baseUrl}/blackbox/events`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${BlackBox.authToken}`,
          'Content-Type': 'application/json',
          'X-Device-ID': BlackBox.deviceId,
          'X-Platform': Platform.OS,
          'X-App-Name': BlackBox.appName,
        },
        body: JSON.stringify(events),
      });
    } catch {
      // Re-add failed events to buffer (capped to avoid memory growth)
      if (BlackBox.buffer.length + events.length <= BlackBox.maxBufferSize * 2) {
        BlackBox.buffer = [...events, ...BlackBox.buffer];
      }
    }
  }

  private static generateDeviceId(): string {
    return 'xxxxxxxx'.replace(/x/g, () =>
      Math.floor(Math.random() * 16).toString(16),
    );
  }
}
