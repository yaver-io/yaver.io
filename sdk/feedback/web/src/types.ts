export interface FeedbackConfig {
  /** Yaver agent URL (e.g., http://192.168.1.100:18080 or relay URL) */
  agentUrl?: string;
  /** Bearer auth token. Optional in 0.2+: omit to use the in-app sign-in modal. */
  authToken?: string;
  /**
   * When true (default), the SDK auto-opens its sign-in modal the first time
   * the user triggers feedback and no `authToken` is provided/cached. Set false
   * to opt out (you must provide `authToken` yourself).
   */
  autoLogin?: boolean;
  /** How to trigger feedback: floating button, keyboard shortcut, or manual only */
  trigger?: 'floating-button' | 'keyboard' | 'manual';
  /** Keyboard shortcut to trigger (default: Ctrl+Shift+F) */
  shortcut?: string;
  /** Whether SDK is enabled (default: true in development) */
  enabled?: boolean;
  /** Max screen recording duration in seconds (default: 120) */
  maxRecordingDuration?: number;
  /** Position of floating button */
  buttonPosition?: 'bottom-right' | 'bottom-left' | 'top-right' | 'top-left';
  /**
   * Max captured errors in ring buffer. Default: 5.
   * Errors are captured via YaverFeedback.attachError() or wrapErrorHandler().
   * The SDK never auto-hooks window.onerror — no conflicts with Sentry, etc.
   */
  maxCapturedErrors?: number;
}

export interface TimelineEvent {
  time: number; // seconds from start
  type: 'voice' | 'screenshot' | 'annotation' | 'console-error';
  text?: string;
  file?: string;
}

export interface DeviceInfo {
  platform: 'web';
  browser: string;
  browserVersion: string;
  os: string;
  screenSize: string;
  userAgent: string;
}

export interface FeedbackBundle {
  metadata: {
    source: 'in-app-sdk';
    deviceInfo: DeviceInfo;
    appVersion?: string;
    url: string; // current page URL
    timeline: TimelineEvent[];
    transcript?: string;
    consoleErrors?: string[];
  };
  video?: Blob;
  audio?: Blob;
  screenshots: Blob[];
  /** Captured errors with stack traces. */
  errors?: CapturedError[];
}

/** An error captured by the SDK's global error handler. */
export interface CapturedError {
  message: string;
  stack: string[];
  isFatal: boolean;
  timestamp: number;
  metadata?: Record<string, unknown>;
}

export interface DiscoveryResult {
  url: string;
  hostname: string;
  version: string;
  latency: number; // ms
}
