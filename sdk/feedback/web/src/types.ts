export interface FeedbackConfig {
  /** Yaver agent URL (e.g., http://192.168.1.100:18080 or relay URL) */
  agentUrl?: string;
  /** Bearer auth token. Optional in 0.2+: omit to use the in-app sign-in modal. */
  authToken?: string;
  /**
   * Override the Yaver public endpoints used for in-SDK auth and cloud-backed
   * device discovery. Defaults to production yaver.io / Convex URLs.
   */
  authConvexSiteUrl?: string;
  authWebBaseUrl?: string;
  /**
   * Convex site URL used for device-backed discovery. When omitted, the SDK
   * uses `authConvexSiteUrl` (or the production default).
   */
  convexUrl?: string;
  /**
   * Preferred device ID from `/devices/list`. If omitted, the SDK can prompt
   * the user to choose one after sign-in.
   */
  preferredDeviceId?: string;
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
  /** Project/app label shown in feedback reports and fix prompts. */
  appName?: string;
  /** Canonical project name for candidate/self-improving flows. */
  projectName?: string;
  /**
   * Optional absolute local project path on the developer machine.
   * Useful when the host app knows exactly which repo/worktree should receive
   * candidate fixes.
   */
  projectPath?: string;
  /** Which surface is being improved. */
  surface?: 'web' | 'mobile' | 'backend';
  /** Which release lane the user is currently exercising. */
  releaseChannel?: 'production' | 'candidate' | 'development';
  /** Candidate deploy metadata for safe self-improving flows. */
  candidate?: FeedbackCandidateConfig;
  /**
   * When true, the SDK immediately asks the agent to create a candidate fix
   * task after the report upload succeeds.
   */
  autoFixOnSend?: boolean;
  /** Called after the report upload succeeds. */
  onReportSent?: (result: FeedbackReportSummary) => void | Promise<void>;
  /**
   * Called when the agent pushes a `reload` command over the command stream.
   * Defaults to `window.location.reload()`.
   */
  onReload?: () => void;
  /**
   * Called when the agent pushes a `reload_bundle` command. Web targets usually
   * map this to a hard page reload unless the host wants custom behavior.
   */
  onReloadBundle?: (bundleUrl?: string, assetsUrl?: string) => void;
  /**
   * Called when the agent streams progress updates (`status` commands) during a
   * reload/build workflow.
   */
  onStatus?: (status: FeedbackStatusUpdate) => void;
}

export interface FeedbackCandidateConfig {
  enabled?: boolean;
  label?: string;
  baseBranch?: string;
  targetBranch?: string;
  previewUrl?: string;
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
    project?: FeedbackProjectRef;
    candidate?: FeedbackCandidateMetadata;
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

export interface FeedbackProjectRef {
  appName?: string;
  projectName?: string;
  projectPath?: string;
  surface?: 'web' | 'mobile' | 'backend';
  releaseChannel?: 'production' | 'candidate' | 'development';
}

export interface FeedbackCandidateMetadata {
  enabled?: boolean;
  label?: string;
  baseBranch?: string;
  targetBranch?: string;
  previewUrl?: string;
}

export interface FeedbackReviewEntry {
  id: string;
  action: 'comment' | 'approve' | 'revert' | 'change_again';
  comment?: string;
  desiredOutcome?: string;
  createdAt: string;
}

export interface FeedbackChangeSet {
  id: string;
  feedbackId: string;
  projectName?: string;
  projectPath?: string;
  surface?: 'web' | 'mobile' | 'backend';
  releaseChannel?: 'production' | 'candidate' | 'development';
  status:
    | 'draft'
    | 'building'
    | 'candidate_ready'
    | 'review_required'
    | 'approved'
    | 'reverted'
    | 'superseded';
  summary?: string;
  candidateLabel?: string;
  candidateUrl?: string;
  baseBranch?: string;
  targetBranch?: string;
  taskId?: string;
  createdAt: string;
  updatedAt: string;
  reviews?: FeedbackReviewEntry[];
}

export interface FeedbackReportSummary {
  id: string;
  changeSet?: FeedbackChangeSet;
}

export interface FeedbackStatusUpdate {
  message: string;
  phase?: string;
  progress?: number;
  at: number;
}

export interface AgentCommand {
  command: string;
  data?: Record<string, unknown>;
}

export interface ReloadAck {
  ok: boolean;
  mode: 'dev' | 'bundle';
  acknowledged: boolean;
  message: string;
  nativeChangesDetected?: boolean;
  changeClass?: string;
}
