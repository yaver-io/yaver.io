/**
 * Remote browser-style sign-in session for a coding-agent CLI on the
 * connected yaver host. Mirrors runnerBrowserAuthSession on the agent
 * Go side. Progression: starting → awaiting_browser (openUrl + code
 * filled) → completed | failed | cancelled.
 */
export interface RunnerBrowserAuthSession {
  id: string;
  runner: string;
  method: string;
  status: 'starting' | 'awaiting_browser' | 'completed' | 'failed' | 'cancelled';
  openUrl?: string;
  code?: string;
  detail?: string;
  authConfigured?: boolean;
  authSource?: string;
  error?: string;
  startedAt: number;
  updatedAt: number;
  completedAt?: number;
}

export interface RunnerAuthStatusRow {
  id: string;
  name: string;
  installed: boolean;
  ready: boolean;
  authConfigured: boolean;
  authSource?: string;
  warning?: string;
  error?: string;
  path?: string;
  detail?: string;
  version?: string;
}

export interface OpenCodeProviderSummary {
  id: string;
  name?: string;
  hasApiKey?: boolean;
  baseUrl?: string;
  models?: Array<{ id: string; name?: string; provider?: string }>;
}

export interface OpenCodeAgentSummary {
  name: string;
  model?: string;
  description?: string;
  isBuiltin?: boolean;
}

export interface OpenCodeConfigSummary {
  path: string;
  exists: boolean;
  defaultAgent?: string;
  model?: string;
  smallModel?: string;
  buildModel?: string;
  planModel?: string;
  providers?: OpenCodeProviderSummary[];
  models?: Array<{ id: string; name?: string; provider?: string }>;
  agents?: OpenCodeAgentSummary[];
  diagnostics?: string[];
}

export interface IncidentEvent {
  id: string;
  timestamp: number;
  severity: 'info' | 'warn' | 'error' | 'fatal';
  category: string;
  code: string;
  source: string;
  title: string;
  userMessage: string;
  technicalInfo?: string;
  suggestedAction?: string;
  operationId?: string;
  deviceId?: string;
  projectPath?: string;
  target?: string;
  logsAvailable: boolean;
  logRefs?: string[];
  correlationId?: string;
  recoverable: boolean;
  metadata?: Record<string, unknown>;
  resolved?: boolean;
}

export interface OperationState {
  id: string;
  kind: string;
  status: string;
  phase?: string;
  message?: string;
  progress?: number;
  deviceId?: string;
  projectPath?: string;
  startedAt: number;
  updatedAt: number;
  incidentIds?: string[];
  metadata?: Record<string, unknown>;
}

export interface CapabilityTargetReadiness {
  enabled: boolean;
  reasonCode?: string;
  reason?: string;
  suggestedAction?: string;
  notes?: string[];
}

export interface CapabilitySnapshot {
  generatedAt: string;
  machine?: Record<string, unknown>;
  infra?: Record<string, unknown>;
  connectivity?: {
    directAvailable?: boolean;
    relayConfigured?: boolean;
    tunnelConfigured?: boolean;
    tailscaleAvailable?: boolean;
  };
  targets: Record<string, CapabilityTargetReadiness>;
}

/**
 * Opt-in config for the overlay's "App Store screenshots" action and the
 * `capture_store_shots` remote command. When set, the SDK can walk the
 * app's routes on-device, screenshot each, and upload them to the agent
 * (which runs the App Store Connect backend).
 */
export interface StoreShotsConfig {
  /** Ordered routes to visit + screenshot (e.g. ['/(tabs)/home', ...]). */
  routes: string[];
  /** Navigation handle: a react-navigation ref or expo-router `router`. */
  navigationRef?: any;
  /** Also set metadata + attempt submit-for-review after upload. */
  submit?: boolean;
  /** Optional per-route screenshot names (defaults to NN_<route>). */
  screens?: string[];
}

export interface FeedbackConfig {
  /** URL of the Yaver agent (e.g. "http://192.168.1.10:18080"). If omitted, auto-discovery is used. */
  agentUrl?: string;
  /**
   * Enables the overlay's "App Store screenshots" action + the
   * `capture_store_shots` remote command. The host supplies the route
   * list (and a navigation ref) once; the SDK captures the real running
   * app — no simulator needed.
   */
  storeShots?: StoreShotsConfig;
  /**
   * Auth token for the Yaver agent. Optional in 0.5+: if omitted, the SDK
   * will hydrate one from AsyncStorage or show its in-app login screen
   * (Apple native / OAuth in-app browser / email) the first time the user
   * triggers feedback.
   */
  authToken?: string;
  /**
   * When true (default), the SDK will automatically prompt the user to sign
   * in and pick a remote machine the first time they trigger feedback and
   * no `authToken` / `preferredDeviceId` is cached. Set false to opt back
   * into the pre-0.5 behavior where you manage auth yourself and pass
   * `authToken` at init.
   */
  autoLogin?: boolean;
  /**
   * Override the public Yaver endpoints the in-app login screen talks to.
   * Useful when running against staging. Defaults to the production
   * yaver.io / Convex site URLs.
   */
  authConvexSiteUrl?: string;
  authWebBaseUrl?: string;
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
  /**
   * App slug used by the in-modal Deploy panel when calling the agent's
   * `/fleet/deploy-options` and `/deploy/ship` endpoints. Should match an
   * `apps[].name` entry in the agent's `yaver.workspace.yaml`. When omitted
   * the panel falls back to the last dot-segment of `bundleId` (e.g.
   * `io.yaver.sfmg` → `sfmg`). Set explicitly when the workspace name
   * differs from the bundleId tail.
   */
  deployAppSlug?: string;
  /**
   * Non-default escape hatch for host apps that want the SDK without
   * shake gesture handling. When enabled:
   * - the SDK does not start ShakeDetector
   * - if `quickIcon` was left as `'auto'`/unset, it is promoted to `'always'`
   * - the app should rely on the draggable quick icon or explicit
   *   `YaverFeedback.startReport()` calls instead
   *
   * Intended for builds where another surface owns motion / haptics.
   */
  disableShakeGesture?: boolean;
  /**
   * Auto-open the BlackBox SSE command channel after init() returns.
   *
   * Default: `true` (0.8.8+). Listens for `reload`, `reload_bundle`,
   * and `status` commands the agent broadcasts after a vibe-coding
   * task commits a fix — drives the auto-reload loop without the host
   * app needing to call `BlackBox.start()` manually.
   *
   * The auto-start is deferred 500ms after init() and gated on having
   * BOTH `agentUrl` and `authToken` resolved, to avoid the iOS 18.3.1
   * rope-string SIGSEGV that the early auto-start in 0.7.6 hit when
   * the agent was in needs-auth mode (tight 401-retry loop in SSE).
   *
   * Set `false` if your host app wants to gate BlackBox start on its
   * own state machine (e.g. only after the user opts into telemetry).
   * Calling `BlackBox.start()` manually is still safe — it's idempotent.
   */
  autoStartBlackBox?: boolean;
  /**
   * Small tap-to-open icon that floats above the app so the user
   * doesn't have to shake every time they want to open feedback.
   * Single tap → open the feedback modal; long-press → menu with
   * "Hide icon" (persisted across launches via AsyncStorage).
   *
   * - `'auto'` (default) → `'after-shake'` on iOS/Android, `'off'` on web.
   * - `'always'` → visible from app launch.
   * - `'after-shake'` → hidden until the first shake this session.
   * - `'off'` → never rendered. Shake still works.
   *
   * The user can always override `'always'` / `'auto'` by long-pressing
   * → Hide. Devs can clear that override via
   * `YaverFeedback.resetQuickIconPreference()`.
   */
  quickIcon?: 'auto' | 'always' | 'after-shake' | 'off';
  /**
   * Deprecated alias for `quickIconBackgroundColor`.
   *
   * Background color for the quick-action icon. The SDK now defaults
   * to a high-visibility safety orange so the icon reads as a utility
   * affordance instead of blending into the common blue/indigo FAB
   * palette many mobile apps already use.
   */
  quickIconColor?: string;
  /**
   * Background color for the quick-action icon.
   * Default: '#ff6b2c' (safety orange).
   */
  quickIconBackgroundColor?: string;
  /**
   * Foreground/text color for the quick-action icon label.
   * Default: '#111111'.
   */
  quickIconForegroundColor?: string;
  /**
   * Border color for the quick-action icon.
   * Default: 'rgba(255,255,255,0.92)'.
   */
  quickIconBorderColor?: string;
  /**
   * Shadow color for the quick-action icon.
   * Default: '#000000'.
   */
  quickIconShadowColor?: string;
  /**
   * Initial position of the quick-action icon, in pixels from the
   * top-left. Default: near the top-right corner of the screen. The
   * icon is draggable at runtime — this is only the first mount.
   */
  quickIconInitialPosition?: { x: number; y: number };
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
   * Callback invoked when the agent pushes a reload command.
   * This happens when the vibe coder triggers a reload from the Yaver mobile app
   * or from another connected device.
   *
   * If not provided, the SDK will attempt `DevSettings.reload()` in dev mode
   * or ignore the command in production.
   *
   * @example
   * onReload: () => {
   *   // Custom reload logic, e.g. re-fetch bundle from agent
   *   Updates.reloadAsync();
   * }
   */
  onReload?: () => void;
  /**
   * Callback invoked when the agent pushes a reload_bundle command with a new
   * native bundle URL. The SDK passes the bundle URL and assets URL so the app
   * can fetch and load the new bundle.
   *
   * If not provided, the SDK will attempt to POST the bundle to localhost:8347
   * (Yaver's on-device HTTP server) for native container reload.
   */
  onReloadBundle?: (bundleUrl: string, assetsUrl?: string) => void;
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
  /**
   * Compile-time lockdown of the auth flow. When true the SDK refuses to
   * ever open the user's external browser (Safari / Chrome) or show a
   * 6-char device code. Auth happens only via native Apple Sign-In
   * (`expo-apple-authentication`), in-app OAuth (`expo-web-browser`'s
   * `ASWebAuthenticationSession` with `preferEphemeralSession: true`), or
   * the built-in email/password form. If a required peer dep is missing,
   * `signInWithOAuth`/`signInWithApple` throw instead of silently falling
   * back to a web redirect.
   *
   * Recommended for apps that already embed OAuth on the native side and
   * never want their users to see a `yaver.io` landing page. This is the
   * belt-and-suspenders version of what the SDK has done since 0.6; set
   * it to guarantee future regressions can't quietly reintroduce a
   * browser-hop fallback.
   *
   * Default: false (preserve historical behavior).
   */
  strictNativeAuth?: boolean;
  /**
   * Optional host invite code to prefill into the in-SDK guest onboarding
   * flow. Useful when your app receives the code from a deep link, QR flow,
   * or an out-of-band host handoff and you want the user to redeem it
   * without typing.
   */
  guestInviteCode?: string;
}

export interface FeedbackBundle {
  metadata: FeedbackMetadata;
  /** Screen-recording file path, when produced by the "Start Recording" action. */
  video?: string;
  /** Optional audio attachment path. */
  audio?: string;
  screenshots: string[];
  /** Captured errors with stack traces, attached via attachError / wrapErrorHandler. */
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
  /** Speech-to-speech provider (legacy), or null. */
  s2sProvider?: string;
  /** Whether the S2S provider is ready for real-time sessions. */
  s2sReady?: boolean;
  /** Speech-to-text provider for transcription, e.g. "deepgram" for Deepgram Flux. */
  sttProvider?: string;
  /** Whether STT is ready (auto-transcription of voice input). */
  sttReady?: boolean;
  /** Text-to-speech provider for readback, e.g. "cartesia". */
  ttsProvider?: string;
  /** Whether TTS readback is ready. */
  ttsReady?: boolean;
  /** Whether the agent-side hands-free task loop is enabled. */
  enabled?: boolean;
  /** Default project slug used by the agent voice loop. */
  defaultProject?: string;
}
