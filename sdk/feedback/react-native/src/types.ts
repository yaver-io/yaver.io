export interface FeedbackConfig {
  /** URL of the Yaver agent (e.g. "http://192.168.1.10:18080"). If omitted, auto-discovery is used. */
  agentUrl?: string;
  /** Auth token for the Yaver agent */
  authToken: string;
  /**
   * Convex site URL for cloud IP resolution.
   * When set, the SDK fetches the agent's IP from Convex instead of
   * requiring a hardcoded agentUrl. Works with cloud machines (CPU/GPU)
   * where the IP is managed by Yaver.
   *
   * Set this OR agentUrl — not both. If both are set, agentUrl wins.
   *
   * @example
   * convexUrl: 'https://your-app.convex.site'
   */
  convexUrl?: string;
  /**
   * Preferred device ID to connect to (from Convex device list).
   * If omitted with convexUrl, connects to the first online device.
   */
  preferredDeviceId?: string;
  /** How feedback collection is triggered */
  trigger?: 'shake' | 'floating-button' | 'manual';
  /** Enable/disable the SDK. Defaults to __DEV__ */
  enabled?: boolean;
  /**
   * Reporting-only mode. When true:
   * - Shake auto-captures screenshot + errors and sends to the agent
   * - No floating button, no feedback modal, no auto-test UI
   * - Agent receives and stores the report but does NOT auto-trigger a fix
   * - Developer reviews reports in CLI and decides what to fix
   *
   * Use case: enable for QA testers / beta users who should report bugs
   * but not trigger code changes. The developer configures their own device
   * with `reportingOnly: false` to get the full SDK with fix triggers.
   *
   * @example
   * // For beta testers:
   * YaverFeedback.init({ reportingOnly: true, trigger: 'shake', ... });
   *
   * // For the developer:
   * YaverFeedback.init({ reportingOnly: false, trigger: 'floating-button', ... });
   */
  reportingOnly?: boolean;
  /** Max screen recording duration in seconds. Default: 120 */
  maxRecordingDuration?: number;
  /**
   * Feedback mode:
   * - 'live': stream events to the agent as they happen
   * - 'narrated': record everything, send on stop
   * - 'batch': dump everything at end (default)
   */
  feedbackMode?: 'live' | 'narrated' | 'batch';
  /**
   * Agent commentary level (0-10).
   * 0 = silent, 10 = agent comments on everything it sees.
   * Only relevant in live mode. Default: 0.
   */
  agentCommentaryLevel?: number;
  /**
   * Enable voice input for feedback annotations. Always true by default.
   * Audio is recorded on the device and sent to the agent for transcription.
   * Works regardless of whether a speech-to-speech provider is configured —
   * if STT is available on the agent, audio is auto-transcribed; otherwise
   * raw audio is attached to the feedback report.
   */
  voiceEnabled?: boolean;
  /**
   * Maximum number of captured errors to keep in memory (ring buffer).
   * Oldest errors are evicted when the buffer is full.
   * Default: 5.
   *
   * Errors are captured via `YaverFeedback.attachError()` or
   * `YaverFeedback.wrapErrorHandler()`. The SDK never auto-hooks global
   * error handlers — this avoids conflicts with Sentry, Crashlytics,
   * Bugsnag, or any other error tracking tool.
   */
  maxCapturedErrors?: number;
  /**
   * Which platforms the Build button targets.
   * - 'ios' — build iOS only
   * - 'android' — build Android only
   * - 'both' — build iOS and Android sequentially (default)
   * - 'web' — build web app
   */
  buildPlatforms?: 'ios' | 'android' | 'both' | 'web';
  /**
   * Auto-deploy builds after successful build.
   * When true (default), iOS builds are uploaded to TestFlight
   * and Android builds are uploaded to Google Play internal testing.
   * When false, builds are created locally without uploading.
   */
  autoDeploy?: boolean;
  /**
   * Background color of the debug console panel.
   * Default: "#2d2d2d" (dark gray). Set to any color to match your app's theme
   * and avoid visual overlap.
   *
   * @example
   * panelBackgroundColor: '#1a1a2e'  // dark blue-gray
   * panelBackgroundColor: '#333333'  // neutral dark gray
   */
  panelBackgroundColor?: string;
  /**
   * TLS certificate fingerprint (SHA256) for HTTPS on LAN.
   * When set, the SDK prefers HTTPS connections and verifies the agent's
   * self-signed cert matches this fingerprint.
   */
  tlsFingerprint?: string;
  /**
   * Require TLS for all connections. When true, refuses HTTP connections.
   * Default: false (HTTPS preferred when fingerprint available, HTTP fallback).
   */
  requireTLS?: boolean;
}

export interface FeedbackBundle {
  metadata: FeedbackMetadata;
  video?: string;
  /** Voice annotation audio file path (WAV). Always available when voiceEnabled. */
  audio?: string;
  /** Transcribed text from voice annotation (if STT/S2S provider is available on agent). */
  audioTranscript?: string;
  screenshots: string[];
  /** Captured errors with stack traces, attached automatically when captureErrors is enabled. */
  errors?: CapturedError[];
}

/** An error captured by the SDK's global error handler. */
export interface CapturedError {
  /** Error message. */
  message: string;
  /** Parsed stack frames (e.g. "at CheckoutButton.handlePress (CheckoutScreen.tsx:47)"). */
  stack: string[];
  /** Whether this was a fatal (unrecoverable) error. */
  isFatal: boolean;
  /** Unix timestamp in milliseconds when the error occurred. */
  timestamp: number;
  /** Optional developer-attached context via YaverFeedback.attachError(). */
  metadata?: Record<string, unknown>;
}

export interface FeedbackMetadata {
  timestamp: string;
  device: DeviceInfo;
  app: AppInfo;
  userNote?: string;
}

export interface DeviceInfo {
  platform: string;
  osVersion: string;
  model: string;
  screenWidth: number;
  screenHeight: number;
}

export interface AppInfo {
  bundleId?: string;
  version?: string;
  buildNumber?: string;
}

export interface TimelineEvent {
  type: 'screenshot' | 'audio' | 'video';
  path: string;
  timestamp: string;
  duration?: number;
}

export interface FeedbackReport {
  id: string;
  bundle: FeedbackBundle;
  status: 'pending' | 'uploading' | 'uploaded' | 'failed';
  error?: string;
}

export interface AgentCommentary {
  id: string;
  timestamp: string;
  message: string;
  type: 'observation' | 'suggestion' | 'question' | 'action';
}

export interface FeedbackStreamEvent {
  type: string;
  timestamp: string;
  data: any;
}

/**
 * A single fix applied by the AI agent during a test session.
 * Displayed in the FixReport component as a markdown-style list.
 */
export interface TestFix {
  /** Unique fix ID */
  id: string;
  /** File that was modified */
  file: string;
  /** Line number of the change */
  line?: number;
  /** Short description of what was fixed */
  description: string;
  /** The error/exception that triggered this fix */
  error?: string;
  /** Diff or code snippet (shown on expand) */
  diff?: string;
  /** Timestamp when the fix was applied */
  timestamp: string;
  /** Whether the fix resolved the issue (verified after hot reload) */
  verified?: boolean;
}

/**
 * Test session state returned by the agent's /test-app/status endpoint.
 */
export interface TestSession {
  /** Whether a test session is currently running */
  active: boolean;
  /** Current screen being tested */
  currentScreen?: string;
  /** Total screens discovered */
  screensDiscovered: number;
  /** Total screens tested */
  screensTested: number;
  /** Errors found during this session */
  errorsFound: number;
  /** Fixes applied (not committed — staged only) */
  fixes: TestFix[];
  /** Session start time */
  startedAt?: string;
  /** Elapsed seconds */
  elapsedSeconds?: number;
  /** Status message */
  status: string;
}

/** Voice capability info returned by the agent's /voice/status endpoint. */
export interface VoiceCapability {
  /** Always true — mobile can always record and send audio. */
  voiceInputEnabled: boolean;
  /** Speech-to-speech provider (e.g. "personaplex", "openai"), or null. */
  s2sProvider?: string;
  /** Whether the S2S provider is ready for real-time sessions. */
  s2sReady?: boolean;
  /** Speech-to-text provider for transcription (e.g. "whisper", "openai"). */
  sttProvider?: string;
  /** Whether STT is ready (auto-transcription of voice input). */
  sttReady?: boolean;
}
